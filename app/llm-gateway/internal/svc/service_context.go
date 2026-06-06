package svc

import (
	"encoding/base64"
	"fmt"

	"github.com/maomeng/aim/app/llm-gateway/internal/config"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/infra"
	"github.com/maomeng/aim/app/llm-gateway/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config      config.Config
	DB          *database.DB
	EncKey      []byte
	ModelRepo   domain.ModelRegistry
	BillingRepo domain.BillingRecorder
	RateLimiter domain.RateLimiter
	Snowflake   *snowflake.Node
	Logger      logx.Logger
	UserClient  userpb.UserServiceClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, nil)
	if err != nil {
		panic("database init failed: " + err.Error())
	}
	if err := db.AutoMigrate(&model.ModelRegistry{}, &model.BillingRecord{}); err != nil {
		panic("auto migrate failed: " + err.Error())
	}

	if c.EncKey == "" {
		panic("encKey not found in config (etcd remote config)")
	}
	encKey, err := base64.StdEncoding.DecodeString(c.EncKey)
	if err != nil {
		panic("encKey decode failed: " + err.Error())
	}
	if len(encKey) != 32 {
		panic("encKey must be 32 bytes (base64 encoded)")
	}

	snowNode, err := snowflake.NewNode(c.Snowflake.WorkerID)
	if err != nil {
		snowNode, err = snowflake.NewNode(0)
		if err != nil {
			panic(fmt.Sprintf("init snowflake failed: %v", err))
		}
	}

	userConn, err := zrpc.NewClient(c.UserService)
	if err != nil {
		panic("user-service client init failed: " + err.Error())
	}

	return &ServiceContext{
		Config:      c,
		DB:          db,
		EncKey:      encKey,
		ModelRepo:   infra.NewModelRepo(db, encKey, c.ModelConf.RefreshInterval),
		BillingRepo: infra.NewBillingRepo(db),
		RateLimiter: infra.NewRateLimiter(c.Redis.Host, c.Redis.Pass, c.RateLimit.DefaultRPM, c.RateLimit.DefaultConcurrency),
		Snowflake:   snowNode,
		Logger:      logger,
		UserClient:  userpb.NewUserServiceClient(userConn.Conn()),
	}
}

func (s *ServiceContext) Close() {
	if s.DB != nil {
		s.DB.Close()
	}
	if s.RateLimiter != nil {
		// rate limiter manages its own redis connection lifecycle
	}
	if s.ModelRepo != nil {
		s.ModelRepo.Close()
	}
}
