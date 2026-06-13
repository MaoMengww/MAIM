package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/maomeng/aim/app/ws-gateway/internal/config"
	"github.com/maomeng/aim/app/ws-gateway/internal/handler"
	"github.com/maomeng/aim/app/ws-gateway/internal/middleware"
	"github.com/maomeng/aim/app/ws-gateway/internal/presence"
	"github.com/maomeng/aim/app/ws-gateway/internal/push"
	"github.com/maomeng/aim/app/ws-gateway/internal/router"
	"github.com/maomeng/aim/app/ws-gateway/internal/session"
	"github.com/maomeng/aim/app/ws-gateway/internal/streamcache"
	"github.com/maomeng/aim/pkg/logx"
	pkgmetrics "github.com/maomeng/aim/pkg/metrics"
	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	message "github.com/maomeng/aim/app/message-service/pb/message"
	pushpb "github.com/maomeng/aim/pkg/pb/push"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/prometheus"
	"github.com/zeromicro/go-zero/core/trace"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/ws-gateway.yaml", "config file")

func main() {
	flag.Parse()

	cfg, err := config.Load(*configFile)
	if err != nil {
		panic(fmt.Sprintf("config load failed: %v", err))
	}

	prometheus.Enable()

	logger := logx.NewLogger(logx.Config{Level: cfg.Log.Level, Format: cfg.Log.Format, Output: cfg.Log.Output})

	trace.StartAgent(trace.Config{
		Name: cfg.Telemetry.Name, Endpoint: cfg.Telemetry.Endpoint,
		Sampler: cfg.Telemetry.Sampler, Disabled: cfg.Telemetry.Disabled,
	})
	defer trace.StopAgent()

	// Redis
	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Host, Password: cfg.Redis.Password, DB: cfg.Redis.DB})

	// Session manager
	sessionMgr := session.NewManager()

	// Push router
	pushRouter := push.NewRouter(sessionMgr)

	// Presence manager
	presenceStore := presence.NewRedisStore(rdb)
	presencePusher := push.NewPresencePusher(pushRouter, sessionMgr)
	presenceMgr := presence.NewManager(presenceStore, presencePusher)

	// Gin engine
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.IDStringToNumber())

	// WebSocket handler
	upgrader := websocket.Upgrader{
		ReadBufferSize:  cfg.WebSocket.ReadBufferSize,
		WriteBufferSize: cfg.WebSocket.WriteBufferSize,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
	// Bot platform gRPC client (for bot token validation on /ws/bot)
	var botPlatClient botplatform.BotPlatformClient
	if len(cfg.BotPlatform.Etcd.Hosts) > 0 || cfg.BotPlatform.Target != "" {
		botPlatClient = botplatform.NewBotPlatformClient(zrpc.MustNewClient(cfg.BotPlatform).Conn())
	}

	// Message service gRPC client (for bot WS reply via message.send)
	var msgClient message.MessageServiceClient
	if len(cfg.MessageService.Etcd.Hosts) > 0 || cfg.MessageService.Target != "" {
		msgClient = message.NewMessageServiceClient(zrpc.MustNewClient(cfg.MessageService).Conn())
	}

	// Stream cache for reconnection replay
	streamCache := streamcache.New(rdb, streamcache.Config{
		Enabled:            cfg.StreamCache.Enabled,
		TTL:                time.Duration(cfg.StreamCache.TTLSeconds) * time.Second,
		MaxChunksPerStream: cfg.StreamCache.MaxChunksPerStream,
	})

	wsHandler := handler.NewWSHandler(upgrader, sessionMgr, presenceMgr, pushRouter, rdb, streamCache, cfg.JWT.Secret, logger, botPlatClient, msgClient)

	// Routes
	router.Register(r, wsHandler, cfg)

	// gRPC server for InternalPushService
	grpcServer := grpc.NewServer()
	pushpb.RegisterInternalPushServiceServer(grpcServer, push.NewPushServer(pushRouter, streamCache))
	reflection.Register(grpcServer)

	wsAddr := fmt.Sprintf("%s:%d", cfg.WebSocket.Host, cfg.WebSocket.Port)
	go func() {
		logger.Infof("ws-gateway-v2 HTTP/WS listening on %s", wsAddr)
		if err := r.Run(wsAddr); err != nil {
			logger.Errorf("gin server error: %v", err)
		}
	}()

	grpcAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	// For etcd registration, resolve actual hostname so other containers can reach us.
	// In K8s/k3s, prefer K8S_SERVICE_NAME env var because pod hostname is not DNS-resolvable.
	registerAddr := grpcAddr
	if cfg.Host == "0.0.0.0" {
		if svcName := os.Getenv("K8S_SERVICE_NAME"); svcName != "" {
			registerAddr = fmt.Sprintf("%s:%d", svcName, cfg.Port)
		} else if h, err := os.Hostname(); err == nil {
			registerAddr = fmt.Sprintf("%s:%d", h, cfg.Port)
		}
	}
	go func() {
		lis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			logger.Errorf("gRPC listen error: %v", err)
			return
		}
		logger.Infof("ws-gateway-v2 gRPC listening on %s", grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			logger.Errorf("gRPC server error: %v", err)
		}
	}()

	// Metrics
	go func() {
		metricsAddr := fmt.Sprintf("0.0.0.0:%d", cfg.Metrics.Port)
		http.Handle("/metrics", pkgmetrics.Handler())
		http.ListenAndServe(metricsAddr, nil)
	}()

	// Etcd registration
	var pub *discov.Publisher
	if cfg.Etcd.Key != "" && len(cfg.Etcd.Hosts) > 0 {
		pub = discov.NewPublisher(cfg.Etcd.Hosts, cfg.Etcd.Key, registerAddr)
		go func() {
			if err := pub.KeepAlive(); err != nil {
				logger.Errorf("etcd publisher keepalive: %v", err)
			}
		}()
		logger.Infof("etcd service published: key=%s addr=%s", cfg.Etcd.Key, registerAddr)
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down ws-gateway-v2...")
	grpcServer.GracefulStop()
	logger.Info("ws-gateway-v2 stopped")
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
