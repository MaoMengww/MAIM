package svc

import (
	"context"
	"fmt"

	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/app/message-service/internal/client"
	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/elasticsearch"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	goredis "github.com/redis/go-redis/v9"

	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config                  config.Config
	DB                      *database.DB
	Redis                   *goredis.Client
	MessageCreatedProducer  *kafka.Producer
	MessageRecalledProducer *kafka.Producer
	MessageEditedProducer   *kafka.Producer
	MessageDeletedProducer  *kafka.Producer
	ESClient                *elasticsearch.Client
	Snowflake               *snowflake.Node
	Logger                  logx.Logger
	ConvClient              client.ConvClient
	UserClient              client.UserClient
	FriendClient            client.FriendClient
	BotPlatformConn         zrpc.Client
	MessageRepo             *repo.MessageRepo
	InboxRepo               *repo.InboxRepo
	BroadcastRepo           *repo.BroadcastRepo
	SequenceRepo            *repo.SequenceRepo
	BotRepo                 *repo.BotRepo
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, logger)
	if err != nil {
		panic(fmt.Sprintf("database init failed: %v", err))
	}
	if err := db.AutoMigrate(&model.Message{}, &model.FailedEvent{}); err != nil {
		panic(fmt.Sprintf("auto migrate failed: %v", err))
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:     c.Redis.Host,
		Password: c.Redis.Pass,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("redis init failed: %v", err))
	}

	kpCreated, err := kafka.NewProducer(c.Kafka, "message.created", logger)
	if err != nil {
		panic(fmt.Sprintf("kafka message.created producer init failed: %v", err))
	}
	kpRecalled, err := kafka.NewProducer(c.Kafka, "message.recalled", logger)
	if err != nil {
		panic(fmt.Sprintf("kafka message.recalled producer init failed: %v", err))
	}
	kpEdited, err := kafka.NewProducer(c.Kafka, "message.edited", logger)
	if err != nil {
		panic(fmt.Sprintf("kafka message.edited producer init failed: %v", err))
	}
	kpDeleted, err := kafka.NewProducer(c.Kafka, "message.deleted", logger)
	if err != nil {
		panic(fmt.Sprintf("kafka message.deleted producer init failed: %v", err))
	}

	esClient, err := elasticsearch.NewClient(c.Elasticsearch)
	if err != nil {
		panic(fmt.Sprintf("elasticsearch init failed: %v", err))
	}
	if err := esClient.EnsureIndex(context.Background(), consts.ESIndexMessages, map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"message_id":  map[string]any{"type": "keyword"},
				"conv_id":     map[string]any{"type": "long"},
				"sender_id":   map[string]any{"type": "long"},
				"sender_type": map[string]any{"type": "keyword"},
				"msg_type":    map[string]any{"type": "integer"},
				"content":     map[string]any{"type": "object", "enabled": false},
				"text": map[string]any{
					"type":     "text",
					"analyzer": "ik_max_word",
					"fields": map[string]any{
						"keyword": map[string]any{"type": "keyword"},
					},
				},
				"created_at": map[string]any{"type": "long"},
			},
		},
	}); err != nil {
		logger.Infof("ensure es index skipped: %v", err)
	}

	sf, err := snowflake.NewNode(c.Snowflake.WorkerID)
	if err != nil {
		panic(fmt.Sprintf("snowflake init failed: %v", err))
	}

	convClient := client.NewConvClient(zrpc.MustNewClient(c.ConvService))
	userClient := client.NewUserClient(zrpc.MustNewClient(c.UserService))
	botPlatformConn := zrpc.MustNewClient(c.BotPlatform)
	friendClient := client.NewFriendClient(zrpc.MustNewClient(c.FriendService))

	return &ServiceContext{
		Config:                  c,
		DB:                      db,
		Redis:                   rdb,
		MessageCreatedProducer:  kpCreated,
		MessageRecalledProducer: kpRecalled,
		MessageEditedProducer:   kpEdited,
		MessageDeletedProducer:  kpDeleted,
		ESClient:                esClient,
		Snowflake:               sf,
		Logger:                  logger,
		ConvClient:              convClient,
		UserClient:              userClient,
		FriendClient:            friendClient,
		BotPlatformConn:         botPlatformConn,
		MessageRepo:             repo.NewMessageRepo(db),
		InboxRepo:               repo.NewInboxRepo(db),
		BroadcastRepo:           repo.NewBroadcastRepo(db),
		SequenceRepo:            repo.NewSequenceRepo(db),
		BotRepo:                 repo.NewBotRepo(db, botplatform.NewBotPlatformClient(botPlatformConn.Conn())),
	}
}
