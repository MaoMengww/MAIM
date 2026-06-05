package svc

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	convpb "github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/app/signaling-service/internal/config"
	"github.com/maomeng/aim/app/signaling-service/internal/consumer"
	"github.com/maomeng/aim/app/signaling-service/internal/model"
	"github.com/maomeng/aim/app/signaling-service/internal/push"
	"github.com/maomeng/aim/app/signaling-service/internal/repo"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/zrpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type ServiceContext struct {
	Config          config.Config
	DB              *gorm.DB
	Redis           *redis.Client
	Fanout          *consumer.Fanout
	Consumer        *consumer.Consumer
	SaramaGroup     sarama.ConsumerGroup
	NotifRepo       *repo.NotificationRepo
	PresenceChecker consumer.PresenceChecker
	PushService     *push.Service
	Logger          logx.Logger
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.NewLogger(logx.Config{Level: "info", Format: "text"})

	rdb := redis.NewClient(&redis.Options{Addr: c.Redis.Host, Password: c.Redis.Pass, DB: 0})

	gdb, err := gorm.Open(postgres.Open(c.Database.DSN), &gorm.Config{})
	if err != nil {
		panic(fmt.Sprintf("postgres connect: %v", err))
	}
	sqlDB, _ := gdb.DB()
	if sqlDB != nil {
		sqlDB.SetMaxOpenConns(c.Database.MaxOpenConn)
		sqlDB.SetMaxIdleConns(c.Database.MaxIdleConn)
	}
	gdb.AutoMigrate(&model.DeviceToken{})

	convClient := convpb.NewConversationServiceClient(zrpc.MustNewClient(c.ConversationService).Conn())
	botPlatformClient := botplatform.NewBotPlatformClient(zrpc.MustNewClient(c.BotPlatform).Conn())
	wsClient := zrpc.MustNewClient(c.WsGateway)

	memberRepo := repo.NewMemberRepo(convClient, botPlatformClient)
	notifRepo := repo.NewNotificationRepo(gdb)
	presenceChecker := consumer.NewRedisPresenceChecker(rdb)
	grpcPusher := consumer.NewGRPCPusher(wsClient)
	botProducer := newBotProducer(c.Kafka.Brokers)
	callbackClient := consumer.NewCallbackClient(logger)
	unreadCache := repo.NewRedisUnreadCache(rdb)
	convRepoS := repo.NewConvRepo(convClient)
	deviceTokenRepo := repo.NewDeviceTokenRepo(gdb)

	// Push service for offline notifications
	var pushSvc *push.Service
	if c.Push.FCMEnabled || c.Push.APNSEnabled {
		var fcmSender *push.FCMSender
		var apnsSender *push.APNSSender
		if c.Push.FCMEnabled && c.Push.FCMCredentials != "" {
			fcmSender, err = push.NewFCMSender(context.Background(), c.Push.FCMCredentials)
			if err != nil {
				logger.Errorf("fcm init failed: %v", err)
			}
		}
		if c.Push.APNSEnabled && c.Push.APNSKeyFile != "" {
			apnsSender, err = push.NewAPNSSender(c.Push.APNSKeyFile, c.Push.APNSKeyID, c.Push.APNSTeamID)
			if err != nil {
				logger.Errorf("apns init failed: %v", err)
			}
		}
		if fcmSender != nil || apnsSender != nil {
			pushSvc = push.NewService(fcmSender, apnsSender, deviceTokenRepo, logger)
		}
	}

	fo := consumer.NewFanout(memberRepo, presenceChecker, grpcPusher, grpcPusher, botProducer, callbackClient, logger)
	fo.SetConvRepo(convRepoS)
	fo.SetUnreadCache(unreadCache)
	if pushSvc != nil {
		fo.SetPushService(pushSvc)
	}

	sigConsumer := consumer.NewConsumer(fo, newDLQProducer(c.Kafka.Brokers), logger)

	saramaCfg := sarama.NewConfig()
	saramaCfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	cg, err := sarama.NewConsumerGroup(c.Kafka.Brokers, c.Kafka.ConsumerGroup, saramaCfg)
	if err != nil {
		panic(fmt.Sprintf("kafka consumer group: %v", err))
	}

	return &ServiceContext{
		Config: c, DB: gdb, Redis: rdb, Fanout: fo,
		Consumer: sigConsumer, SaramaGroup: cg, NotifRepo: notifRepo,
		PresenceChecker: presenceChecker, PushService: pushSvc, Logger: logger,
	}
}

func (ctx *ServiceContext) StartKafkaConsumer(kafkaCtx context.Context) {
	go func() {
		for {
			select {
			case <-kafkaCtx.Done():
				return
			default:
				if err := ctx.SaramaGroup.Consume(kafkaCtx, ctx.Consumer.Topics(), ctx.Consumer); err != nil {
					ctx.Logger.Errorf("kafka consume: %v", err)
				}
				if kafkaCtx.Err() != nil {
					return
				}
				time.Sleep(time.Second)
			}
		}
	}()
}

func newBotProducer(brokers []string) *botKafkaAdapter {
	cfg := sarama.NewConfig()
	cfg.Producer.RequiredAcks = sarama.WaitForLocal
	cfg.Producer.Return.Successes = true
	p, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return &botKafkaAdapter{}
	}
	return &botKafkaAdapter{producer: p}
}

func newDLQProducer(brokers []string) *dlqAdapter {
	cfg := sarama.NewConfig()
	cfg.Producer.RequiredAcks = sarama.WaitForLocal
	cfg.Producer.Return.Successes = true
	p, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return &dlqAdapter{}
	}
	return &dlqAdapter{producer: p}
}

type botKafkaAdapter struct {
	producer sarama.SyncProducer
}

func (a *botKafkaAdapter) Send(_ context.Context, key string, value []byte) error {
	if a.producer == nil {
		return nil
	}
	_, _, err := a.producer.SendMessage(&sarama.ProducerMessage{
		Topic: "bot.event.ai", Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(value),
	})
	return err
}

type dlqAdapter struct {
	producer sarama.SyncProducer
}

func (a *dlqAdapter) Send(_ context.Context, topic string, key string, value []byte) error {
	if a.producer == nil {
		return nil
	}
	_, _, err := a.producer.SendMessage(&sarama.ProducerMessage{
		Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(value),
	})
	return err
}
