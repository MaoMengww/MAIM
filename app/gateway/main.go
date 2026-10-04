package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/maomeng/aim/app/gateway/internal/config"
	"github.com/maomeng/aim/app/gateway/internal/grpc"
	"github.com/maomeng/aim/app/gateway/internal/router"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/prometheus"
	"github.com/zeromicro/go-zero/core/trace"
)

var configFile = flag.String("f", "etc/gateway.yaml", "config file")

func main() {
	flag.Parse()

	var cfg config.Config
	conf.MustLoad(*configFile, &cfg, conf.UseEnv())

	prometheus.Enable()

	logger := logx.NewLogger(logx.Config{
		Level:    cfg.Log.Level,
		Format:   cfg.Log.Format,
		Output:   cfg.Log.Output,
		FilePath: cfg.Log.FilePath,
		MaxSize:  cfg.Log.MaxSize,
		Name:     cfg.Name,
		Path:     cfg.Log.Path,
	})

	trace.StartAgent(trace.Config{
		Name:     cfg.Telemetry.Name,
		Endpoint: cfg.Telemetry.Endpoint,
		Sampler:  cfg.Telemetry.Sampler,
		Disabled: cfg.Telemetry.Disabled,
	})
	defer trace.StopAgent()

	jwtMgr := jwt.NewManager(cfg.JWT.Secret, cfg.JWT.ExpireSec, cfg.JWT.RefreshSec)

	logger.Info("initializing gRPC clients via platform DNS...")
	clients := grpc.NewClients(&cfg)
	defer clients.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Host,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rdb.Ping(ctx).Err(); err != nil {
			logger.Errorf("redis ping failed (rate limiting disabled): %v", err)
		}
	}
	defer rdb.Close()

	r := router.New(&cfg, clients, jwtMgr, rdb, logger)

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	go func() {
		logger.Infof("gateway listening on %s", addr)
		fmt.Printf("gateway started on %s\n", addr)
		if err := r.Run(addr); err != nil {
			logger.Errorf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down gateway...")
	logger.Info("gateway stopped")
}
