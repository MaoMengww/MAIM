package svc

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/ai-bot-service/internal/config"
	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"

	"github.com/maomeng/aim/pkg/interceptor"
	goredis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

type ServiceContext struct {
	Config          config.Config
	DB              *database.DB
	Redis           *goredis.Client
	Logger          logx.Logger
	KafkaProducer   *kafka.Producer
	LlmGatewayConn  zrpc.Client
	MessageSvcConn  zrpc.Client
	KnowledgeConn   zrpc.Client
	WsGatewayConn   zrpc.Client
	BotPlatformConn zrpc.Client
	UserServiceConn zrpc.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, logger)
	_ = db.AutoMigrate(&model.Memory{})
	if err != nil {
		panic(fmt.Sprintf("database init failed: %v", err))
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:     c.Redis.Host,
		Password: c.Redis.Pass,
		DB:       0,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("redis init failed: %v", err))
	}

	reqIDClientOpt := zrpc.WithDialOption(
		grpc.WithChainUnaryInterceptor(interceptor.UnaryRequestIDClientInterceptor()),
	)
	reqIDStreamOpt := zrpc.WithDialOption(
		grpc.WithChainStreamInterceptor(interceptor.StreamRequestIDClientInterceptor()),
	)
	llmGatewayConn, err := zrpc.NewClient(c.LlmGateway, reqIDClientOpt, reqIDStreamOpt)
	if err != nil {
		panic(fmt.Sprintf("llm-gateway client failed: %v", err))
	}
	messageSvcConn, err := zrpc.NewClient(c.MessageService, reqIDClientOpt, reqIDStreamOpt)
	if err != nil {
		panic(fmt.Sprintf("message-service client failed: %v", err))
	}
	knowledgeConn, err := zrpc.NewClient(c.KnowledgeBase, reqIDClientOpt, reqIDStreamOpt)
	if err != nil {
		panic(fmt.Sprintf("knowledge-base client failed: %v", err))
	}
	wsGatewayConn, err := zrpc.NewClient(c.WsGateway, reqIDClientOpt, reqIDStreamOpt)
	if err != nil {
		panic(fmt.Sprintf("ws-gateway client failed: %v", err))
	}
	botPlatformConn, err := zrpc.NewClient(c.BotPlatform, reqIDClientOpt, reqIDStreamOpt)
	if err != nil {
		panic(fmt.Sprintf("bot-platform client failed: %v", err))
	}
	userServiceConn, err := zrpc.NewClient(c.UserService, reqIDClientOpt, reqIDStreamOpt)
	if err != nil {
		panic(fmt.Sprintf("user-service client failed: %v", err))
	}

	return &ServiceContext{
		Config:          c,
		DB:              db,
		Redis:           rdb,
		Logger:          logger,
		LlmGatewayConn:  llmGatewayConn,
		MessageSvcConn:  messageSvcConn,
		KnowledgeConn:   knowledgeConn,
		WsGatewayConn:   wsGatewayConn,
		BotPlatformConn: botPlatformConn,
		UserServiceConn: userServiceConn,
	}
}
