package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/realtime-service/internal/config"
	"github.com/maomeng/aim/app/realtime-service/internal/handler"
	"github.com/maomeng/aim/app/realtime-service/internal/metrics"
	"github.com/maomeng/aim/app/realtime-service/internal/middleware"
	"github.com/maomeng/aim/app/realtime-service/internal/offline"
	"github.com/maomeng/aim/app/realtime-service/internal/repo"
	"github.com/maomeng/aim/app/realtime-service/internal/router"
	"github.com/maomeng/aim/app/realtime-service/internal/server"
	"github.com/maomeng/aim/app/realtime-service/internal/session"
	"github.com/maomeng/aim/app/realtime-service/internal/streamcache"
	"github.com/maomeng/aim/app/realtime-service/internal/svc"
	"github.com/maomeng/aim/app/realtime-service/internal/transport"
	"github.com/maomeng/aim/app/realtime-service/pb/realtime"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/migrations/postgres"
	registry "github.com/maomeng/aim/pkg/connections"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	pkgmetrics "github.com/maomeng/aim/pkg/metrics"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/prometheus"
	"github.com/zeromicro/go-zero/core/trace"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

var configFile = flag.String("f", "etc/realtime.yaml", "config file")

func main() {
	flag.Parse()
	var cfg config.Config
	conf.MustLoad(*configFile, &cfg, conf.UseEnv())
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(cfg config.Config) error {
	var shutdownDeadline *time.Timer
	// Registered before all cleanup defers so the deadline also bounds resource
	// cleanup (not only socket drain) during a dependency outage.
	defer func() {
		if shutdownDeadline != nil {
			shutdownDeadline.Stop()
		}
	}()
	prometheus.Enable()
	if cfg.Registry.InstanceID == "" {
		cfg.Registry.InstanceID = os.Getenv("HOSTNAME")
		if cfg.Registry.InstanceID == "" {
			name, err := os.Hostname()
			if err != nil {
				return err
			}
			cfg.Registry.InstanceID = name
		}
	}
	if cfg.Registry.TTLSeconds <= cfg.WebSocket.HeartbeatInterval || cfg.WebSocket.HeartbeatInterval <= 0 || cfg.WebSocket.WriteTimeoutSeconds <= 0 || cfg.WebSocket.MaxConn <= 0 {
		return errors.New("invalid realtime connection timing or limit")
	}
	if cfg.Shutdown.ReadinessDelaySeconds < 0 || cfg.Shutdown.DrainSeconds < 0 || cfg.Shutdown.TimeoutSeconds < cfg.Shutdown.ReadinessDelaySeconds+cfg.Shutdown.DrainSeconds+5 {
		return errors.New("shutdown timeout must allow readiness delay, drain, and five seconds of resource cleanup")
	}
	logger := logx.NewLogger(logx.Config{Level: cfg.Log.Level, Format: cfg.Log.Format, Output: cfg.Log.Output, Name: cfg.Name})
	trace.StartAgent(trace.Config{Name: cfg.Telemetry.Name, Endpoint: cfg.Telemetry.Endpoint, Sampler: cfg.Telemetry.Sampler, Disabled: cfg.Telemetry.Disabled})
	defer trace.StopAgent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Host, Password: cfg.Redis.Password, DB: cfg.Redis.DB})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return err
	}
	db, err := database.NewDB(cfg.Database, logger)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := database.RunMigrations(db.DB, postgres.FS); err != nil {
		return err
	}
	var fcm *offline.FCMSender
	var apns *offline.APNSSender
	if cfg.Push.FCMEnabled {
		fcm, err = offline.NewFCMSender(ctx, cfg.Push.FCMCredentials)
		if err != nil {
			return err
		}
	}
	if cfg.Push.APNSEnabled {
		apns, err = offline.NewAPNSSender(cfg.Push.APNSKeyFile, cfg.Push.APNSKeyID, cfg.Push.APNSTeamID)
		if err != nil {
			return err
		}
	}
	push := offline.NewService(fcm, apns, repo.NewDeviceTokenRepo(db.DB), logger, cfg.Push.APNSTopic)
	store := registry.New(rdb, time.Duration(cfg.Registry.TTLSeconds)*time.Second)
	sessions := session.NewManager(cfg.WebSocket.MaxConn)
	cache := streamcache.New(rdb, streamcache.Config{Enabled: cfg.StreamCache.Enabled, TTL: time.Duration(cfg.StreamCache.TTLSeconds) * time.Second, MaxChunksPerStream: cfg.StreamCache.MaxChunksPerStream})
	node := &transport.Router{Redis: rdb, Registry: store, Sessions: sessions, Cache: cache, Offline: push, Logger: logger, InstanceID: cfg.Registry.InstanceID, WriteTimeout: time.Duration(cfg.WebSocket.WriteTimeoutSeconds) * time.Second}
	if err := node.Subscribe(ctx); err != nil {
		return err
	}
	defer node.Close()
	botRPC, err := zrpc.NewClient(cfg.BotService)
	if err != nil {
		return err
	}
	defer botRPC.Conn().Close()
	msgRPC, err := zrpc.NewClient(cfg.MessageService)
	if err != nil {
		return err
	}
	defer msgRPC.Conn().Close()
	userRPC, err := zrpc.NewClient(cfg.UserService)
	if err != nil {
		return err
	}
	defer userRPC.Conn().Close()
	consumer, err := kafka.NewConsumer(cfg.Kafka, []string{delivery.Topic}, cfg.Kafka.ConsumerGroup, logger)
	if err != nil {
		return err
	}
	defer consumer.Close()
	kafkaCtx, kafkaCancel := context.WithCancel(ctx)
	defer kafkaCancel()
	consumeHandler := &kafka.MessageHandler{Logger: logger, OnMessage: func(ctx context.Context, _ []byte, raw []byte) error {
		start := time.Now()
		defer func() { metrics.KafkaProcessingSeconds.Observe(time.Since(start).Seconds(), delivery.Topic) }()
		metrics.KafkaMessagesTotal.Inc(delivery.Topic)
		var intent delivery.Intent
		if err := json.Unmarshal(raw, &intent); err != nil {
			return err
		}
		return node.Deliver(ctx, intent)
	}}
	failures := make(chan error, 4)
	var wg sync.WaitGroup
	wg.Go(func() {
		for kafkaCtx.Err() == nil {
			if err := consumer.Consume(kafkaCtx, consumeHandler); err != nil && kafkaCtx.Err() == nil {
				logger.Errorf("delivery consumer: %v", err)
				select {
				case <-kafkaCtx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}
	})
	defer func() { kafkaCancel(); wg.Wait() }()
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery(), middleware.RequestID())
	ws := &handler.WSHandler{Router: node, Config: cfg, BotClient: botpb.NewBotServiceClient(botRPC.Conn()), MessageClient: message.NewMessageServiceClient(msgRPC.Conn()), UserClient: userpb.NewUserServiceClient(userRPC.Conn()), Upgrader: websocket.Upgrader{ReadBufferSize: cfg.WebSocket.ReadBufferSize, WriteBufferSize: cfg.WebSocket.WriteBufferSize, CheckOrigin: func(*http.Request) bool { return true }}}
	router.Register(engine, ws)
	httpServer := &http.Server{Addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), Handler: engine, ReadHeaderTimeout: 5 * time.Second}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", pkgmetrics.Handler())
	metricsServer := &http.Server{Addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Metrics.Port)), Handler: metricsMux, ReadHeaderTimeout: 5 * time.Second}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(identityInterceptor))
	realtime.RegisterRealtimeServiceServer(grpcServer, server.NewRealtimeServiceServer(&svc.ServiceContext{NotifRepo: repo.NewNotificationRepo(db.DB), PresenceChecker: store, PushService: push, Router: node}))
	reflection.Register(grpcServer)
	wsListener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return err
	}
	defer wsListener.Close()
	grpcListener, err := net.Listen("tcp", cfg.ListenOn)
	if err != nil {
		return err
	}
	defer grpcListener.Close()
	metricsListener, err := net.Listen("tcp", metricsServer.Addr)
	if err != nil {
		return err
	}
	defer metricsListener.Close()
	go func() {
		if err := httpServer.Serve(wsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failures <- err
		}
	}()
	go func() {
		if err := metricsServer.Serve(metricsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failures <- err
		}
	}()
	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil {
			failures <- err
		}
	}()
	logger.Infof("realtime-service ready: node=%s http=%s grpc=%s metrics=%s", cfg.Registry.InstanceID, httpServer.Addr, cfg.ListenOn, metricsServer.Addr)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(quit)
	select {
	case <-quit:
	case err = <-failures:
		logger.Errorf("listener failed: %v", err)
	}
	node.StopAccepting()
	logger.Info("realtime readiness withdrawn; connection drain pending")
	shutdownDeadline = time.AfterFunc(time.Duration(cfg.Shutdown.TimeoutSeconds)*time.Second, func() {
		logger.Errorf("realtime.shutdown_deadline_exceeded: forcing exit after %ds", cfg.Shutdown.TimeoutSeconds)
		os.Exit(1)
	})
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Duration(cfg.Shutdown.TimeoutSeconds)*time.Second)
	defer shutdownCancel()
	delay := time.NewTimer(time.Duration(cfg.Shutdown.ReadinessDelaySeconds) * time.Second)
	select {
	case <-delay.C:
	case <-shutdownCtx.Done():
		delay.Stop()
	}
	node.Drain(shutdownCtx, time.Duration(cfg.Shutdown.DrainSeconds)*time.Second)
	node.WaitConnections(shutdownCtx)
	kafkaCancel()
	cancel()
	_ = node.Close()
	stopDone := make(chan struct{})
	go func() { grpcServer.GracefulStop(); close(stopDone) }()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		grpcServer.Stop()
	}
	_ = httpServer.Shutdown(shutdownCtx)
	_ = metricsServer.Shutdown(shutdownCtx)
	logger.Info("realtime-service stopped")
	return err
}
func identityInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("user-id")
	if len(values) == 0 && strings.HasSuffix(info.FullMethod, "/PushNotification") {
		return next(ctx, req)
	}
	if len(values) != 1 {
		return nil, status.Error(codes.Unauthenticated, "missing user identity")
	}
	if err := identity.Validate(values[0]); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid user identity")
	}
	return next(ctx, req)
}
