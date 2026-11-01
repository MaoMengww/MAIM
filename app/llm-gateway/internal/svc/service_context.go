package svc

import (
	"encoding/base64"
	"time"

	"github.com/maomeng/aim/app/llm-gateway/internal/config"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/infra"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config        config.Config
	DB            *database.DB
	EncKey        []byte
	ModelRepo     domain.ModelRegistry
	BillingRepo   domain.BillingRecorder
	Capacity      domain.CapacityScheduler
	CapacityRedis redis.UniversalClient
	Logger        logx.Logger
	UserClient    userpb.UserServiceClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.DefaultLogger()
	if c.IngestEmbeddingToken == "" {
		panic("ingestEmbeddingToken is required")
	}

	db, err := database.NewDB(c.Database, nil)
	if err != nil {
		panic("database init failed: " + err.Error())
	}
	if err := database.RunMigrations(db.DB, postgres.FS); err != nil {
		panic("run migrations failed: " + err.Error())
	}

	if c.EncKey == "" {
		panic("encKey not found in local configuration")
	}
	encKey, err := base64.StdEncoding.DecodeString(c.EncKey)
	if err != nil {
		panic("encKey decode failed: " + err.Error())
	}
	if len(encKey) != 32 {
		panic("encKey must be 32 bytes (base64 encoded)")
	}

	userConn, err := zrpc.NewClient(c.UserService)
	if err != nil {
		panic("user-service client init failed: " + err.Error())
	}
	capacityRedis := redis.NewClient(&redis.Options{
		Addr: c.Redis.Host, Password: c.Redis.Pass,
		MaxRetries: -1, ContextTimeoutEnabled: true,
		DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
	})
	capacity, err := infra.NewCapacityScheduler(capacityRedis, infra.CapacityConfig{
		MaxInFlight:   c.Capacity.MaxInFlight,
		LeaseDuration: c.Capacity.LeaseDuration, PollInterval: c.Capacity.PollInterval,
	})
	if err != nil {
		_ = capacityRedis.Close()
		panic("capacity scheduler init failed: " + err.Error())
	}

	return &ServiceContext{
		Config:        c,
		DB:            db,
		EncKey:        encKey,
		ModelRepo:     infra.NewModelRepo(db, encKey, c.ModelConf.RefreshInterval),
		BillingRepo:   infra.NewBillingRepo(db),
		Capacity:      capacity,
		CapacityRedis: capacityRedis,
		Logger:        logger,
		UserClient:    userpb.NewUserServiceClient(userConn.Conn()),
	}
}

func (s *ServiceContext) Close() {
	if s.Capacity != nil {
		if err := s.Capacity.Close(); err != nil {
			s.Logger.Errorf("capacity scheduler close failed: %v", err)
		}
	}
	if s.CapacityRedis != nil {
		_ = s.CapacityRedis.Close()
	}
	if s.DB != nil {
		if err := s.DB.Close(); err != nil {
			s.Logger.Errorf("database close failed: %v", err)
		}
	}
	if s.ModelRepo != nil {
		s.ModelRepo.Close()
	}
}
