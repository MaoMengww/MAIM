package svc

import (
	"context"
	"fmt"

	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/app/conversation-service/internal/client"
	"github.com/maomeng/aim/app/conversation-service/internal/config"
	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/repo"
	messagepb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	goredis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config           config.Config
	DB               *database.DB
	Redis            *goredis.Client
	Snowflake        *snowflake.Node
	Logger           logx.Logger
	Repo             repo.RepoInterface
	UnreadCache      *repo.UnreadCache
	UnreadProducer   *kafka.Producer
	BotEventProducer *kafka.Producer
	BotPlatformRpc   botplatform.BotPlatformClient
	UserClient       *client.UserClient
	MessageRpc       messagepb.MessageServiceClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, logger)
	if err != nil {
		panic(fmt.Sprintf("database init failed: %v", err))
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.ConversationMember{}, &model.ConvReadSeq{}, &model.ConvSettings{}, &model.ConvBot{}); err != nil {
		panic(fmt.Sprintf("auto migrate failed: %v", err))
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:     c.Redis.Host,
		Password: c.Redis.Pass,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("redis init failed: %v", err))
	}

	sf, err := snowflake.NewNode(c.Snowflake.WorkerID)
	if err != nil {
		panic(fmt.Sprintf("snowflake init failed: %v", err))
	}

	var botPlatformRpc botplatform.BotPlatformClient
	if c.BotPlatform.Etcd.Hosts != nil || c.BotPlatform.Endpoints != nil {
		botPlatformRpc = botplatform.NewBotPlatformClient(zrpc.MustNewClient(c.BotPlatform).Conn())
		logger.Infof("bot-platform rpc client initialized")
	}

	var messageRpc messagepb.MessageServiceClient
	if c.MessageService.Etcd.Hosts != nil || c.MessageService.Endpoints != nil {
		messageRpc = messagepb.NewMessageServiceClient(zrpc.MustNewClient(c.MessageService).Conn())
		logger.Infof("message-service rpc client initialized")
	}

	var userClient *client.UserClient
	if c.UserService.Etcd.Hosts != nil || c.UserService.Endpoints != nil {
		userClient = client.NewUserClient(zrpc.MustNewClient(c.UserService))
		logger.Infof("user-service rpc client initialized")
	}

	r := repo.NewRepo(db, botPlatformRpc)

	// 初始化未读数缓存
	unreadCache := repo.NewUnreadCache(rdb)

	var unreadProducer *kafka.Producer
	if len(c.Kafka.Brokers) > 0 {
		var err error
		unreadProducer, err = kafka.NewProducer(c.Kafka, consts.KafkaTopicConversationReadUpdated, logger)
		if err != nil {
			logger.Errorf("init unread producer failed: %v", err)
		}
	}

	// Kafka producer for bot events
	var botEventProducer *kafka.Producer
	if len(c.Kafka.Brokers) > 0 {
		var err error
		botEventProducer, err = kafka.NewProducer(c.Kafka, consts.KafkaTopicConvBotAdded, logger)
		if err != nil {
			logger.Errorf("init bot event producer failed: %v", err)
		}
	}

	return &ServiceContext{
		Config:           c,
		DB:               db,
		Redis:            rdb,
		Snowflake:        sf,
		Logger:           logger,
		Repo:             r,
		UnreadCache:      unreadCache,
		UnreadProducer:   unreadProducer,
		BotEventProducer: botEventProducer,
		BotPlatformRpc:   botPlatformRpc,
		UserClient:       userClient,
		MessageRpc:       messageRpc,
	}
}
