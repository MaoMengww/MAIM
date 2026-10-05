package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/maomeng/aim/app/bot-service/internal/client"
	"github.com/maomeng/aim/app/bot-service/internal/config"
	"github.com/maomeng/aim/app/bot-service/internal/consumer"
	"github.com/maomeng/aim/app/bot-service/internal/graph"
	"github.com/maomeng/aim/app/bot-service/internal/memory"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/server"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var role = flag.String("role", "all", "service role: all, control, runtime")

var configFile = flag.String("f", "etc/bot.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	if *role != "all" && *role != "control" && *role != "runtime" {
		panic("invalid -role: " + *role)
	}
	c.Role = *role
	ctx := svc.NewServiceContext(c)
	logger := logx.DefaultLogger()
	defer ctx.DeliveryPublisher.Close()

	// gRPC server — also initializes OpenTelemetry tracing via go-zero's ServiceConf.SetUp()
	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		botpb.RegisterBotServiceServer(grpcServer, server.NewBotServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(
		interceptor.UnaryErrorInterceptor(),
		interceptor.UnaryRequestIDInterceptor(),
		interceptor.UnaryUserIDInterceptor(),
	)

	var eventConsumer *kafka.Consumer
	kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
	var thirdPartyConsumer *kafka.Consumer
	if c.Role != "runtime" {
		var err error
		thirdPartyConsumer, err = kafka.NewConsumer(c.Kafka, []string{consts.KafkaTopicMessageCreated, consts.KafkaTopicMessageEdited, consts.KafkaTopicMessageRecalled, consts.KafkaTopicConvBotAdded}, c.Kafka.ConsumerGroup+"-third-party", logger)
		if err != nil {
			panic(fmt.Sprintf("third-party consumer init failed: %v", err))
		}
		h := consumer.NewThirdPartyHandler(ctx.BotRepo, repo.NewConvBotRepo(ctx.DB), ctx.DeliveryPublisher, logger)
		go func() {
			for kafkaCtx.Err() == nil {
				if err := thirdPartyConsumer.Consume(kafkaCtx, h); err != nil && kafkaCtx.Err() == nil {
					logger.Errorf("third-party consumer: %v", err)
					select {
					case <-kafkaCtx.Done():
						return
					case <-time.After(time.Second):
					}
				}
			}
		}()
	}
	if c.Role != "control" {
		// Register global eino callback handlers for OTel tracing and logging
		callbacks.AppendGlobalHandlers(graph.NewOTelCallbackHandler(logger))

		// Kafka consumer for message.created events
		var err error
		eventConsumer, err = kafka.NewConsumer(c.Kafka, []string{consts.KafkaTopicMessageCreated}, c.Kafka.ConsumerGroup, logger)
		if err != nil {
			panic(fmt.Sprintf("kafka consumer init failed: %v", err))
		}

		// Start Kafka consumer lag monitoring
		if kafkaClient := eventConsumer.GetClient(); kafkaClient != nil {
			kafka.CollectConsumerLag(kafkaClient, c.Kafka.ConsumerGroup, []string{consts.KafkaTopicMessageCreated})
		}

		db := ctx.DB
		userNames := graph.UserNamesFunc(func(ctx context.Context, ids []int64) (map[int64]string, error) {
			type userInfo struct {
				ID       int64 `gorm:"column:id"`
				Username string
			}
			var users []userInfo
			if err := db.Table("user.users").Where("id IN ?", ids).Find(&users).Error; err != nil {
				return nil, err
			}
			m := make(map[int64]string, len(users))
			for _, u := range users {
				m[u.ID] = u.Username
			}
			return m, nil
		})

		llmClient := client.NewLlmGatewayClient(ctx.LlmGatewayConn)
		msgClient := client.NewMessageClient(ctx.MessageSvcConn)
		kbClient := client.NewKnowledgeClient(ctx.KnowledgeConn)

		deliveryClient := client.NewDeliveryClient(msgClient, ctx.DeliveryPublisher)

		memStore := memory.NewGraphStoreAdapter(ctx.MemoryManager)

		handler := consumer.NewHandler(
			logger,
			ctx.BotRepo,
			repo.NewConvBotRepo(ctx.DB),
			llmClient,
			nil,      // retriever
			memStore, // memoryStore
			ctx.MemoryManager,
			msgClient,
			kbClient,
			nil, // convClient
			consumer.NewDedup(ctx.Redis),
			deliveryClient,
			userNames,
		)

		// Start Kafka consumer after tracing is initialized (by MustNewServer above)
		// so ConsumerClaim spans have a valid global TracerProvider.
		go func() {
			kafkaHandler := &kafka.MessageHandler{
				OnMessage: func(ctx context.Context, key, value []byte) error {
					return handler.Handle(ctx, value)
				},
				Logger: logger,
			}
			if err := eventConsumer.Consume(kafkaCtx, kafkaHandler); err != nil {
				logger.Errorf("kafka consumer stopped: %v", err)
			}
		}()

	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		logger.Info("shutting down...")
		kafkaCancel()
		if eventConsumer != nil {
			eventConsumer.Close()
		}
		if thirdPartyConsumer != nil {
			_ = thirdPartyConsumer.Close()
		}
		if ctx.Neo4jDriver != nil {
			_ = ctx.Neo4jDriver.Close(context.Background())
		}
		s.Stop()
	}()

	fmt.Printf("Starting bot-service (%s) rpc server at %s...\n", c.Role, c.ListenOn)
	s.Start()
}
