package svc

import (
	"fmt"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	infraMinio "github.com/maomeng/aim/app/knowledge-base/internal/infra/minio"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/repo"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/delivery"
	pkgkafka "github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	pkgminio "github.com/maomeng/aim/pkg/minio"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"
)

type ServiceContext struct {
	Config            config.Config
	DB                *database.DB
	KBRepo            domain.KBRepo
	DocRepo           domain.DocumentRepo
	FileStore         domain.FileStore
	Producer          *pkgkafka.Producer
	DeliveryPublisher *delivery.Publisher
	LLMGatewayClient  zrpc.Client
	Snowflake         *snowflake.Node
}

func NewServiceContext(c config.Config, role string) *ServiceContext {
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

	var kafkaProducer *pkgkafka.Producer
	if role == "online" {
		kafkaProducer, err = pkgkafka.NewProducer(c.Kafka, "document.uploaded", log)
		if err != nil {
			panic(fmt.Sprintf("init kafka producer failed: %v", err))
		}
	}
	deliveryPublisher, err := delivery.NewPublisher(c.Kafka, log)
	if err != nil {
		panic(fmt.Sprintf("init delivery publisher failed: %v", err))
	}

	minioClient, err := pkgminio.NewClient(c.Minio)
	if err != nil {
		panic(fmt.Sprintf("init minio client failed: %v", err))
	}
	fileStore := infraMinio.NewFileStore(minioClient)

	var snowNode *snowflake.Node
	if role == "online" {
		snowNode, err = snowflake.NewNode(c.Snowflake.WorkerID)
		if err != nil {
			panic(fmt.Sprintf("init snowflake failed: %v", err))
		}
	}

	kbRepo := repo.NewKBRepo(db).WithSnow(snowNode)
	docRepo := repo.NewDocumentRepo(db).WithSnow(snowNode)
	llmGatewayClient := zrpc.MustNewClient(c.LLMGateway)

	return &ServiceContext{
		Config:            c,
		DB:                db,
		KBRepo:            kbRepo,
		DocRepo:           docRepo,
		FileStore:         fileStore,
		Producer:          kafkaProducer,
		DeliveryPublisher: deliveryPublisher,
		LLMGatewayClient:  llmGatewayClient,
		Snowflake:         snowNode,
	}
}

func (s *ServiceContext) Close() {
	if s.DeliveryPublisher != nil {
		_ = s.DeliveryPublisher.Close()
	}
	if s.Producer != nil {
		_ = s.Producer.Close()
	}
	if s.LLMGatewayClient != nil {
		_ = s.LLMGatewayClient.Conn().Close()
	}
	if s.DB != nil {
		_ = s.DB.Close()
	}
}
