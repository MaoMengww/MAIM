package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudwego/eino/callbacks"
	"github.com/maomeng/aim/app/ai-bot-service/internal/client"
	"github.com/maomeng/aim/app/ai-bot-service/internal/config"
	"github.com/maomeng/aim/app/ai-bot-service/internal/consumer"
	"github.com/maomeng/aim/app/ai-bot-service/internal/graph"
	"github.com/maomeng/aim/app/ai-bot-service/internal/memory"
	"github.com/maomeng/aim/app/ai-bot-service/internal/repo"
	"github.com/maomeng/aim/app/ai-bot-service/internal/server"
	"github.com/maomeng/aim/app/ai-bot-service/internal/svc"
	"github.com/maomeng/aim/app/ai-bot-service/pb/aibot"
	"github.com/maomeng/aim/pkg/consts"
	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// memoryStoreAdapter wraps memory.PgRepo to implement graph.MemoryStore.
type memoryStoreAdapter struct {
	repo *memory.PgRepo
}

func (a *memoryStoreAdapter) Retrieve(ctx context.Context, botID, userID int64, query string, limit int) ([]graph.MemoryItem, error) {
	items, err := a.repo.SearchByUser(ctx, botID, userID, query, limit)
	if err != nil {
		return nil, err
	}
	result := make([]graph.MemoryItem, len(items))
	for i, item := range items {
		result[i] = graph.MemoryItem{
			ID:         item.ID,
			Content:    item.Content,
			Type:       item.MemoryType,
			Importance: item.Importance,
		}
	}
	return result, nil
}

var configFile = flag.String("f", "etc/ai-bot-service.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	configcenter.InitConfigCenter(c.Name, c.Etcd.Hosts, &c)
	ctx := svc.NewServiceContext(c)
	logger := logx.DefaultLogger()

	// Register global eino callback handlers for OTel tracing and logging
	callbacks.AppendGlobalHandlers(graph.NewOTelCallbackHandler(logger))

	// Kafka consumer for message.created events
	eventConsumer, err := kafka.NewConsumer(c.Kafka, []string{consts.KafkaTopicMessageCreated}, c.Kafka.ConsumerGroup, logger)
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
		if err := db.Table("users").Where("id IN ?", ids).Find(&users).Error; err != nil {
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

	wsClient := client.NewWsGatewayClient(ctx.WsGatewayConn)

	memRepo := memory.NewPgRepo(ctx.DB)
	memStore := memoryStoreAdapter{repo: memRepo}

	handler := consumer.NewHandler(
		logger,
		repo.NewBotRepo(ctx.DB, botplatform.NewBotPlatformClient(ctx.BotPlatformConn.Conn())),
		repo.NewConvBotRepo(ctx.DB),
		ctx.DB,
		llmClient,
		nil,       // retriever
		&memStore, // memoryStore
		msgClient,
		kbClient,
		nil, // convClient
		consumer.NewDedup(ctx.Redis),
		wsClient,
		userNames,
	)

	// gRPC server — also initializes OpenTelemetry tracing via go-zero's ServiceConf.SetUp()
	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		aibot.RegisterAiBotServiceServer(grpcServer, server.NewAiBotServiceServer(ctx))
		aibot.RegisterConversationToolServiceServer(grpcServer, server.NewConversationToolServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(
		interceptor.UnaryErrorInterceptor(),
		interceptor.UnaryRequestIDInterceptor(),
		interceptor.UnaryUserIDInterceptor(),
	)

	// Start Kafka consumer after tracing is initialized (by MustNewServer above)
	// so ConsumerClaim spans have a valid global TracerProvider.
	kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
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

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		logger.Info("shutting down...")
		kafkaCancel()
		eventConsumer.Close()
		s.Stop()
	}()

	fmt.Printf("Starting ai-bot-service rpc server at %s...\n", c.ListenOn)
	s.Start()
}
