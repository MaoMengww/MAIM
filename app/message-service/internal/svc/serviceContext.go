package svc

import (
	"cmp"
	"context"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/dispatcher"
	"github.com/maomeng/aim/app/message-service/internal/es"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	goredis "github.com/redis/go-redis/v9"
)

// ServiceContext holds the message domain: conversations and members, the
// message content store, per-user inboxes, sequence allocation, read positions,
// the outbox and the unread read model. Everything the send and read paths need
// — membership, members, conversation ids, latest message, unread — is local.
type ServiceContext struct {
	Config                 config.Config
	DB                     *database.DB
	Redis                  *goredis.Client
	MessageCreatedProducer *kafka.Producer
	MessageDeletedProducer *kafka.Producer
	BotEventProducer       *kafka.Producer
	DeliveryPublisher      *delivery.Publisher
	ESClient               *es.Client
	Snowflake              *snowflake.Node
	Logger                 logx.Logger
	MessageRepo            *repo.MessageRepo
	InboxRepo              *repo.InboxRepo
	BroadcastRepo          *repo.BroadcastRepo
	SequenceRepo           *repo.SequenceRepo
	OutboxRepo             *repo.OutboxRepo
	OutboxDispatcher       *dispatcher.OutboxDispatcher
	ConversationRepo       *repo.ConversationRepo
	ProfileRepo            *repo.ProfileRepo

	// SendSystemMessage emits a system message through the same in-process send
	// path as a user message. It is injected by main so the conversation logic
	// does not import the message logic package, which would be an import cycle.
	SendSystemMessage func(ctx context.Context, req *message.SendSystemMessageReq) error
}

func NewServiceContext(c config.Config) *ServiceContext {
	c.Message.InboxRetentionDays = cmp.Or(c.Message.InboxRetentionDays, 30)
	if c.Message.InboxRetentionDays < 0 {
		panic("message.inboxRetentionDays must be positive")
	}
	logger := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, logger)
	if err != nil {
		panic(fmt.Sprintf("database init failed: %v", err))
	}
	// Migrations run before AutoMigrate: the conversation tables must be moved
	// into the msg schema before GORM creates anything with the new names.
	if err := database.RunMigrations(db.DB, postgres.FS); err != nil {
		panic(fmt.Sprintf("run migrations failed: %v", err))
	}
	if err := db.AutoMigrate(
		&model.Message{}, &model.Sequence{}, &model.OutboxEvent{},
		&model.Conversation{}, &model.ConversationMember{}, &model.ConvReadSeq{},
		&model.ConvSettings{}, &model.ConvBot{},
	); err != nil {
		panic(fmt.Sprintf("auto migrate failed: %v", err))
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:     c.Redis.Host,
		Password: c.Redis.Pass,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("redis init failed: %v", err))
	}

	kpCreated, err := kafka.NewProducer(c.Kafka, consts.KafkaTopicMessageCreated, logger)
	if err != nil {
		panic(fmt.Sprintf("kafka message.created producer init failed: %v", err))
	}
	kpDeleted, err := kafka.NewProducer(c.Kafka, consts.KafkaTopicMessageDeleted, logger)
	if err != nil {
		panic(fmt.Sprintf("kafka message.deleted producer init failed: %v", err))
	}

	var botEventProducer *kafka.Producer
	if len(c.Kafka.Brokers) > 0 {
		botEventProducer, err = kafka.NewProducer(c.Kafka, consts.KafkaTopicConvBotAdded, logger)
		if err != nil {
			logger.Errorf("init bot event producer failed: %v", err)
		}
	}

	deliveryPublisher, err := delivery.NewPublisher(c.Kafka, logger)
	if err != nil {
		panic(fmt.Sprintf("delivery publisher init failed: %v", err))
	}
	esClient, err := es.NewClient(c.Elasticsearch)
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

	return &ServiceContext{
		Config:                 c,
		DB:                     db,
		Redis:                  rdb,
		MessageCreatedProducer: kpCreated,
		MessageDeletedProducer: kpDeleted,
		BotEventProducer:       botEventProducer,
		DeliveryPublisher:      deliveryPublisher,
		ESClient:               esClient,
		Snowflake:              sf,
		Logger:                 logger,
		MessageRepo:            repo.NewMessageRepo(db),
		InboxRepo:              repo.NewInboxRepo(db),
		BroadcastRepo:          repo.NewBroadcastRepo(db),
		SequenceRepo:           repo.NewSequenceRepo(db),
		OutboxRepo:             repo.NewOutboxRepo(db),
		ConversationRepo:       repo.NewConversationRepo(db),
		ProfileRepo:            repo.NewProfileRepo(db),
		OutboxDispatcher: dispatcher.NewOutboxDispatcher(
			repo.NewOutboxRepo(db),
			map[string]*kafka.Producer{
				consts.KafkaTopicMessageCreated: kpCreated,
				consts.KafkaTopicMessageDeleted: kpDeleted,
			},
			logger,
		),
	}
}

func (s *ServiceContext) Close() {
	if s.DeliveryPublisher != nil {
		_ = s.DeliveryPublisher.Close()
	}
	for _, p := range []*kafka.Producer{s.MessageCreatedProducer, s.MessageDeletedProducer, s.BotEventProducer} {
		if p != nil {
			_ = p.Close()
		}
	}
	if s.Redis != nil {
		_ = s.Redis.Close()
	}
	if s.DB != nil {
		_ = s.DB.Close()
	}
}
