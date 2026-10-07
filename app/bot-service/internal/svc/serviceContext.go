package svc

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/app/bot-service/internal/client"
	"github.com/maomeng/aim/app/bot-service/internal/config"
	"github.com/maomeng/aim/app/bot-service/internal/memory"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/identity"
	pkgjwt "github.com/maomeng/aim/pkg/jwt"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"

	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	goredis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

type ServiceContext struct {
	Repo              repo.BotRepoInterface
	BotRepo           *repo.BotRepo
	EncryptionKey     []byte
	BotJWT            *pkgjwt.Manager
	MessageClient     msgpb.MessageServiceClient
	Config            config.Config
	DB                *database.DB
	Redis             *goredis.Client
	Logger            logx.Logger
	KafkaProducer     *kafka.Producer
	Neo4jDriver       neo4j.DriverWithContext
	MemoryStore       *memory.Neo4jStore
	MemoryManager     *memory.Manager
	MemoryVector      *memory.MemoryVectorStore
	MemoryEmbedder    memory.Embedder
	LlmGatewayConn    zrpc.Client
	MessageSvcConn    zrpc.Client
	KnowledgeConn     zrpc.Client
	DeliveryPublisher *delivery.Publisher
	RuntimeClient     botpb.BotServiceClient
	UserServiceConn   zrpc.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, logger)
	if err != nil {
		panic(fmt.Sprintf("database init failed: %v", err))
	}
	if err := database.RunMigrations(db.DB, postgres.FS); err != nil {
		panic(fmt.Sprintf("run migrations failed: %v", err))
	}

	r := repo.NewBotRepo(db)
	base := &ServiceContext{Config: c, DB: db, Logger: logger, Repo: r, BotRepo: r,
		EncryptionKey: []byte(c.EncryptionKey), BotJWT: pkgjwt.NewManager(c.JWT.Secret, c.JWT.ExpireSec, c.JWT.RefreshSec)}
	base.DeliveryPublisher, err = delivery.NewPublisher(c.Kafka, logger)
	if err != nil {
		panic(fmt.Sprintf("delivery publisher init failed: %v", err))
	}
	if c.Role != "runtime" {
		ctx := context.Background()
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "aim:bot:official-templates").Error; err != nil {
				return fmt.Errorf("lock official templates: %w", err)
			}
			return ensureSeedTemplates(ctx, repo.NewBotRepo(&database.DB{DB: tx}))
		}); err != nil {
			panic(fmt.Sprintf("official template initialization failed: %v", err))
		}
		base.MessageClient = msgpb.NewMessageServiceClient(zrpc.MustNewClient(c.MessageService).Conn())
	}
	if c.Role == "control" {
		if c.Runtime.Target == "" {
			panic("Runtime.Target is required for the control role")
		}
		base.RuntimeClient = botpb.NewBotServiceClient(zrpc.MustNewClient(c.Runtime).Conn())
		return base
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

	memoryEmbedder := memory.NewGatewayEmbedder(llmGatewayConn)

	var memoryVector *memory.MemoryVectorStore
	if c.Milvus.Host != "" {
		vecStore, vecErr := memory.NewMemoryVectorStore(c.Milvus, c.Memory.VectorCollection, c.Memory.EmbeddingDim)
		if vecErr != nil {
			panic(fmt.Sprintf("memory vector store init failed: %v", vecErr))
		}
		if err := vecStore.EnsureCollection(context.Background()); err != nil {
			panic(fmt.Sprintf("memory vector collection ensure failed: %v", err))
		}
		memoryVector = vecStore
	}

	memoryManager := memory.NewManager(logger, memoryStore, nil, memoryEmbedder, memoryVector)
	memoryManager.SetVectorTopK(c.Memory.VectorTopKMult)
	memoryManager.SetProfileChat(func(ctx context.Context, modelID string, modelName string, ownerID *string, botID string, prompt string) (string, error) {
		llmClient := client.NewLlmGatewayClient(llmGatewayConn)
		chatModel := llmClient.NewEinoChatModel(modelID, modelName, ownerID, botID)
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
	userServiceConn, err := zrpc.NewClient(c.UserService, reqIDClientOpt, reqIDStreamOpt)
	if err != nil {
		panic(fmt.Sprintf("user-service client failed: %v", err))
	}

	base.Redis = rdb
	base.Neo4jDriver = neo4jDriver
	base.MemoryStore = memoryStore
	base.MemoryManager = memoryManager
	base.MemoryVector = memoryVector
	base.MemoryEmbedder = memoryEmbedder
	base.LlmGatewayConn = llmGatewayConn
	base.MessageSvcConn = messageSvcConn
	base.KnowledgeConn = knowledgeConn
	base.UserServiceConn = userServiceConn
	return base
}

func ensureSeedTemplates(ctx context.Context, r repo.BotRepoInterface) error {
	bots, err := r.ListOfficialTemplates(ctx)
	if err != nil {
		return fmt.Errorf("list official templates: %w", err)
	}

	hasQA := false
	hasKnowledge := false
	for _, b := range bots {
		switch b.TemplateID {
		case "qa":
			hasQA = true
		case "knowledge":
			hasKnowledge = true
		}
	}

	if !hasQA {
		if err := createTemplateBot(ctx, r, "qa", "智能问答助手",
			"你是一个智能问答助手，请用中文回答用户的问题。", 0.7, 10, false); err != nil {
			return err
		}
	}

	if !hasKnowledge {
		if err := createTemplateBot(ctx, r, "knowledge", "知识库问答助手",
			"你是一个知识库问答助手。基于提供的知识库内容回答用户问题。如果知识库中没有相关信息，请如实告知。", 0.3, 15, true); err != nil {
			return err
		}
	}
	return nil
}

func createTemplateBot(ctx context.Context, r repo.BotRepoInterface, templateID, name, systemPrompt string, temperature float64, maxContextMessages int, enableKnowledge bool) error {
	id, err := identity.New()
	if err != nil {
		return fmt.Errorf("create template bot %s identity: %w", templateID, err)
	}

	bot := &model.Bot{
		ID:                 id,
		OwnerType:          "platform",
		Name:               name,
		Type:               consts.BotTypeOfficial,
		TemplateID:         templateID,
		Status:             consts.BotStatusActive,
		UsePlatformModel:   true,
		SystemPrompt:       systemPrompt,
		EnableKnowledge:    enableKnowledge,
		Temperature:        temperature,
		MaxContextMessages: maxContextMessages,
		ConnMode:           "ws",
		Settings:           []byte(`{"prompt_locale":"zh-CN"}`),
	}

	if err := r.CreateBot(ctx, bot); err != nil {
		return fmt.Errorf("create template bot %s: %w", templateID, err)
	}
	return nil
}
