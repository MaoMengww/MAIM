package svc

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/app/ai-bot-service/internal/client"
	"github.com/maomeng/aim/app/ai-bot-service/internal/config"
	"github.com/maomeng/aim/app/ai-bot-service/internal/memory"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"

	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
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
	Neo4jDriver     neo4j.DriverWithContext
	MemoryStore     *memory.Neo4jStore
	MemoryManager   *memory.Manager
	MemoryVector    *memory.MemoryVectorStore
	MemoryEmbedder  memory.Embedder
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

	neo4jDriver, err := neo4j.NewDriverWithContext(c.Memory.Neo4j.URI, neo4j.BasicAuth(c.Memory.Neo4j.Username, c.Memory.Neo4j.Password, ""))
	if err != nil {
		panic(fmt.Sprintf("neo4j driver init failed: %v", err))
	}
	if err := neo4jDriver.VerifyConnectivity(context.Background()); err != nil {
		panic(fmt.Sprintf("neo4j connectivity failed: %v", err))
	}
	memoryStore := memory.NewNeo4jStore(neo4jDriver, c.Memory.Neo4j.Database, logger)
	if err := memoryStore.InitSchema(context.Background()); err != nil {
		panic(fmt.Sprintf("neo4j memory schema init failed: %v", err))
	}
	memoryIDGen, err := snowflake.NewNode(c.Memory.WorkerID)
	if err != nil {
		panic(fmt.Sprintf("memory snowflake init failed: %v", err))
	}

	memoryEmbedder := memory.NewGatewayEmbedder(llmGatewayConn)

	var memoryVector *memory.MemoryVectorStore
	if c.Milvus.Host != "" {
		vecStore, vecErr := memory.NewMemoryVectorStore(c.Milvus, c.Memory.VectorCollection, c.Memory.EmbeddingDim)
		if vecErr != nil {
			logger.Errorf("memory vector store init failed, hybrid retrieval disabled: %v", vecErr)
		} else if err := vecStore.EnsureCollection(context.Background()); err != nil {
			logger.Errorf("memory vector collection ensure failed, hybrid retrieval disabled: %v", err)
		} else {
			memoryVector = vecStore
		}
	}

	memoryManager := memory.NewManager(logger, memoryStore, nil, memoryIDGen, memoryEmbedder, memoryVector)
	memoryManager.SetVectorTopK(c.Memory.VectorTopKMult)
	memoryManager.SetProfileChat(func(ctx context.Context, modelID int64, modelName string, ownerID int64, prompt string) (string, error) {
		llmClient := client.NewLlmGatewayClient(llmGatewayConn)
		chatModel := llmClient.NewEinoChatModel(modelID, modelName, ownerID)
		msgs := []*schema.Message{{Role: schema.User, Content: prompt}}
		resp, err := chatModel.Generate(ctx, msgs)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	})

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
		Neo4jDriver:     neo4jDriver,
		MemoryStore:     memoryStore,
		MemoryManager:   memoryManager,
		MemoryVector:    memoryVector,
		MemoryEmbedder:  memoryEmbedder,
		LlmGatewayConn:  llmGatewayConn,
		MessageSvcConn:  messageSvcConn,
		KnowledgeConn:   knowledgeConn,
		WsGatewayConn:   wsGatewayConn,
		BotPlatformConn: botPlatformConn,
		UserServiceConn: userServiceConn,
	}
}
