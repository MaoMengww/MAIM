package svc

import (
	"github.com/maomeng/aim/app/user-service/internal/config"
	authlogic "github.com/maomeng/aim/app/user-service/internal/logic/auth"
	friendlogic "github.com/maomeng/aim/app/user-service/internal/logic/friend"
	userlogic "github.com/maomeng/aim/app/user-service/internal/logic/user"
	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/app/user-service/internal/repo"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type ServiceContext struct {
	Config config.Config
	DB     *database.DB
	Redis  *redis.Redis
	JWT    *jwt.Manager
	Snow   *snowflake.Node
	Log    logx.Logger

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

	snowNode, err := snowflake.NewNode(7)
	if err != nil {
		snowNode, _ = snowflake.NewNode(0)
	}

	userRepo := repo.NewUserRepo(db)
	authRepo := repo.NewAuthRepo(db, rdb)
	userLogic := userlogic.New(userRepo, logger)

	return &ServiceContext{
		Config:        cfg,
		DB:            db,
		Redis:         rdb,
		JWT:           jwtMgr,
		Snow:          snowNode,
		Log:           logger,
		AuthLogic:     authlogic.New(userRepo, authRepo, snowNode, jwtMgr, logger),
		UserLogic:     userLogic,
		StatusLogic:   userlogic.NewStatusLogic(rdb),
		FriendContext: friendlogic.NewContext(db, snowNode, userLogic),
	}
}

func (s *ServiceContext) Close() {
	if s.DB != nil {
		_ = s.DB.Close()
	}
}
