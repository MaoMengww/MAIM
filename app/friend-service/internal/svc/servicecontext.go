package svc

import (
	"github.com/maomeng/aim/app/friend-service/internal/client"
	"github.com/maomeng/aim/app/friend-service/internal/config"
	"github.com/maomeng/aim/app/friend-service/internal/model"
	"github.com/maomeng/aim/app/friend-service/internal/repo"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config            config.Config
	DB                *database.DB
	Snowflake         *snowflake.Node
	UserClient        client.UserClientInterface
	FriendRequestRepo repo.FriendRequestRepoInterface
	FriendRepo        repo.FriendRepoInterface
	FriendGroupRepo   repo.FriendGroupRepoInterface
	BlockRepo         repo.BlockRepoInterface
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := database.NewDB(c.Database, nil)
	if err != nil {
		panic("failed to init database: " + err.Error())
	}
	if err := db.AutoMigrate(&model.Friend{}, &model.FriendGroup{}, &model.FriendRequest{}, &model.UserBlock{}); err != nil {
		panic("auto migrate failed: " + err.Error())
	}

	sn, err := snowflake.NewNode(4)
	if err != nil {
		panic("failed to init snowflake: " + err.Error())
	}

	userCli := client.NewUserClient(zrpc.MustNewClient(c.UserRPC))

	return &ServiceContext{
		Config:            c,
		DB:                db,
		Snowflake:         sn,
		UserClient:        userCli,
		FriendRequestRepo: repo.NewFriendRequestRepo(db),
		FriendRepo:        repo.NewFriendRepo(db),
		FriendGroupRepo:   repo.NewFriendGroupRepo(db),
		BlockRepo:         repo.NewBlockRepo(db),
	}
}
