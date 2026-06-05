package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/maomeng/aim/app/signaling-service/internal/config"
	"github.com/maomeng/aim/app/signaling-service/internal/server"
	"github.com/maomeng/aim/app/signaling-service/internal/svc"
	"github.com/maomeng/aim/app/signaling-service/pb/signaling"
	"github.com/maomeng/aim/pkg/metrics"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/signaling.yaml", "config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	ctx := svc.NewServiceContext(c)

	kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
	defer kafkaCancel()
	ctx.StartKafkaConsumer(kafkaCtx)

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Handler())

	// Device token registration
	if ctx.PushService != nil {
		metricsMux.HandleFunc("/api/v1/device/register", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", 405)
				return
			}
			var req struct {
				DeviceID string `json:"device_id"`
				Platform string `json:"platform"`
				Token    string `json:"token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			userIDStr := r.Header.Get("X-User-ID")
			userID, _ := strconv.ParseInt(userIDStr, 10, 64)
			provider := "fcm"
			if req.Platform == "ios" {
				provider = "apns"
			}
			if err := ctx.PushService.RegisterDevice(r.Context(), userID, req.DeviceID, req.Platform, req.Token, provider); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(200)
		})

		metricsMux.HandleFunc("/api/v1/device/unregister", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", 405)
				return
			}
			var req struct {
				DeviceID string `json:"device_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			userIDStr := r.Header.Get("X-User-ID")
			userID, _ := strconv.ParseInt(userIDStr, 10, 64)
			if err := ctx.PushService.UnregisterDevice(r.Context(), userID, req.DeviceID); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(200)
		})
	}

	metricsAddr := fmt.Sprintf("0.0.0.0:9094")
	metricsSrv := &http.Server{Addr: metricsAddr, Handler: metricsMux}
	go func() {
		ctx.Logger.Infof("metrics server listening on %s", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			ctx.Logger.Errorf("metrics server: %v", err)
		}
	}()

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		signaling.RegisterSignalingServiceServer(grpcServer, server.NewSignalingServiceServer(ctx))
		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	defer s.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	ctx.Logger.Infof("signaling-service started on %s", c.ListenOn)
	fmt.Printf("signaling-service started on %s\n", c.ListenOn)

	s.Start()

	<-quit
	ctx.Logger.Info("shutting down signaling-service...")
	kafkaCancel()
	if ctx.SaramaGroup != nil {
		_ = ctx.SaramaGroup.Close()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)
	ctx.Logger.Info("signaling-service stopped")
}
