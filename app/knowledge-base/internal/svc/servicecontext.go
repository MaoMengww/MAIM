package svc

import (
	"fmt"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/chunker"
	infraMinio "github.com/maomeng/aim/app/knowledge-base/internal/infra/minio"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/parser"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/repo"
	"github.com/maomeng/aim/pkg/database"
	pkgkafka "github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	pkgminio "github.com/maomeng/aim/pkg/minio"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config           config.Config
	DB               *database.DB
	KBRepo           domain.KBRepo
	DocRepo          domain.DocumentRepo
	FileStore        domain.FileStore
	Parser           domain.Parser
	Chunker          domain.Chunker
	Producer         *pkgkafka.Producer
	LLMGatewayClient zrpc.Client
	Snowflake        *snowflake.Node
}

func NewServiceContext(c config.Config) *ServiceContext {
	log := logx.DefaultLogger()
	log.Infof("Initializing knowledge-base service...")

	db, err := database.NewDB(c.Database, log)
	if err != nil {
		panic(fmt.Sprintf("init database failed: %v", err))
	}
	if err := db.AutoMigrate(&domain.KnowledgeBase{}, &domain.Document{}, &domain.ChunkRecord{}, &domain.KnowledgeBinding{}); err != nil {
		panic(fmt.Sprintf("auto migrate failed: %v", err))
	}
	log.Infof("Database connected")

	kafkaProducer, err := pkgkafka.NewProducer(c.Kafka, "document.uploaded", log)
	if err != nil {
		panic(fmt.Sprintf("init kafka producer failed: %v", err))
	}

	minioClient, err := pkgminio.NewClient(c.Minio)
	if err != nil {
		panic(fmt.Sprintf("init minio client failed: %v", err))
	}
	fileStore := infraMinio.NewFileStore(minioClient)

	snowNode, err := snowflake.NewNode(c.Snowflake.WorkerID)
	if err != nil {
		snowNode, err = snowflake.NewNode(0)
		if err != nil {
			panic(fmt.Sprintf("init snowflake failed: %v", err))
		}
	}

	kbRepo := repo.NewKBRepo(db).WithSnow(snowNode)
	docRepo := repo.NewDocumentRepo(db).WithSnow(snowNode)
	llmGatewayClient := zrpc.MustNewClient(c.LLMGateway)
	defaultParser := parser.NewParserChain(parser.NewBuiltinParser("txt"))
	defaultChunker := chunker.NewChunker()

	return &ServiceContext{
		Config:           c,
		DB:               db,
		KBRepo:           kbRepo,
		DocRepo:          docRepo,
		FileStore:        fileStore,
		Parser:           defaultParser,
		Chunker:          defaultChunker,
		Producer:         kafkaProducer,
		LLMGatewayClient: llmGatewayClient,
		Snowflake:        snowNode,
	}
}
