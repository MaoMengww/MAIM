package svc

import (
	"github.com/maomeng/aim/app/user-service/internal/config"
	authlogic "github.com/maomeng/aim/app/user-service/internal/logic/auth"
	friendlogic "github.com/maomeng/aim/app/user-service/internal/logic/friend"
	userlogic "github.com/maomeng/aim/app/user-service/internal/logic/user"
	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/app/user-service/internal/repo"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/connections"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	goredis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type ServiceContext struct {
	Config        config.Config
	DB            *database.DB
	Redis         *redis.Redis
	PresenceRedis goredis.UniversalClient
	JWT           *jwt.Manager
	Log           logx.Logger

	AuthLogic     *authlogic.Logic
	UserLogic     *userlogic.Logic
	StatusLogic   *userlogic.StatusLogic
	FriendContext *friendlogic.Context
}

func NewServiceContext(cfg config.Config) *ServiceContext {
	logger := logx.DefaultLogger()

	db, err := database.NewDB(cfg.Database, logger)
	if err != nil {
		panic("database init failed: " + err.Error())
	}
	if cfg.Database.Driver == "postgres" {
		if err := database.RunMigrations(db.DB, postgres.FS); err != nil {
			panic("run migrations failed: " + err.Error())
		}
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserDevice{}, &model.Friend{}, &model.FriendGroup{}, &model.FriendRequest{}, &model.UserBlock{}); err != nil {
		panic("auto migrate failed: " + err.Error())
	}

	var rdb *redis.Redis
	if cfg.AppRedis.ClusterMode {
		rdb, err = redis.NewRedis(redis.RedisConf{
			Host: cfg.AppRedis.Host,
			Pass: cfg.AppRedis.Password,
			Type: redis.ClusterType,
		})
	} else {
		rdb, err = redis.NewRedis(redis.RedisConf{
			Host: cfg.AppRedis.Host,
			Pass: cfg.AppRedis.Password,
			Type: redis.NodeType,
		})
	}
	if err != nil {
		panic("redis init failed: " + err.Error())
	}

	jwtMgr := jwt.NewManager(cfg.JWT.Secret, cfg.JWT.ExpireSec, cfg.JWT.RefreshSec)

	userRepo := repo.NewUserRepo(db)
	authRepo := repo.NewAuthRepo(db, rdb)
	userLogic := userlogic.New(userRepo, logger)
	presenceRedis := goredis.NewUniversalClient(&goredis.UniversalOptions{
		Addrs: []string{cfg.AppRedis.Host}, Password: cfg.AppRedis.Password,
		DB: cfg.AppRedis.DB, IsClusterMode: cfg.AppRedis.ClusterMode,
	})

	return &ServiceContext{
		Config:        cfg,
		DB:            db,
		Redis:         rdb,
		PresenceRedis: presenceRedis,
		JWT:           jwtMgr,
		Log:           logger,
		AuthLogic:     authlogic.New(userRepo, authRepo, jwtMgr, logger),
		UserLogic:     userLogic,
		StatusLogic:   userlogic.NewStatusLogic(connections.New(presenceRedis, 0)),
		FriendContext: friendlogic.NewContext(db, userLogic),
	}
}

func (s *ServiceContext) Close() {
	if s.PresenceRedis != nil {
		_ = s.PresenceRedis.Close()
	}
	if s.DB != nil {
		_ = s.DB.Close()
	}
}
