package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maomeng/aim/app/gateway/internal/config"
	"github.com/maomeng/aim/app/gateway/internal/grpc"
	"github.com/maomeng/aim/app/gateway/internal/router"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/prometheus"
	"github.com/zeromicro/go-zero/core/trace"
)

var configFile = flag.String("f", "etc/gateway.yaml", "config file")

func main() {
	flag.Parse()

	cfg, err := config.Load(*configFile)
	if err != nil {
		panic(fmt.Sprintf("config load failed: %v", err))
	}

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

	// --- Config Center ---
	initConfigCenter(cfg, logger)
	// ---------------------

	logger.Info("initializing gRPC clients via etcd service discovery...")
	clients := grpc.NewClients(cfg)
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

	r := router.New(cfg, clients, jwtMgr, rdb, logger)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
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

func initConfigCenter(cfg *config.Config, logger logx.Logger) {
	if len(cfg.Etcd.Hosts) == 0 {
		logger.Info("config center: no etcd hosts configured, skip")
		return
	}

	key := configcenter.ConfigKey(cfg.Name)
	ss, err := configcenter.NewEtcdSubscriber(cfg.Etcd.Hosts, key)
	if err != nil {
		logger.Infof("config center: cannot connect to etcd (%v), use local config only", err)
		return
	}

	// Load remote config on startup (non-fatal if unavailable)
	raw, err := ss.Value()
	if err == nil && raw != "" {
		if err := configcenter.MergeRemote(cfg, []byte(raw)); err != nil {
			logger.Errorf("config center: merge remote config failed: %v", err)
		} else {
			logger.Infof("config center: loaded remote config from etcd key=%s", key)
		}
	}

	// Watch for config changes and hot-reload
	if err := ss.AddListener(func() {
		raw, err := ss.Value()
		if err != nil || raw == "" {
			return
		}
		// Re-merge into the shared config (components reading cfg at runtime pick up changes)
		if err := configcenter.MergeRemote(cfg, []byte(raw)); err != nil {
			logger.Errorf("config center: hot-reload merge failed: %v", err)
			return
		}
		logger.Infof("config center: config hot-reloaded from etcd key=%s", key)
	}); err != nil {
		logger.Errorf("config center: add listener failed: %v", err)
	}
}
