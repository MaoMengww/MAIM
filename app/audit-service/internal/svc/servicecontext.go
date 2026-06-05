package svc

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/audit-service/internal/config"
	"github.com/maomeng/aim/app/audit-service/internal/repo"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/minio"
	"github.com/maomeng/aim/pkg/snowflake"
)

type ServiceContext struct {
	Config    config.Config
	DB        *database.DB
	AuditRepo *repo.AuditRepo
	Snowflake *snowflake.Node
	KafkaProd *kafka.Producer
	MinIO     *minio.Client
	Logger    logx.Logger
}

func NewServiceContext(c config.Config) *ServiceContext {
	logger := logx.DefaultLogger()

	db, err := database.NewDB(c.Database, logger)
	if err != nil {
		panic(fmt.Sprintf("database init failed: %v", err))
	}

	sf, err := snowflake.NewNode(c.Snowflake.WorkerID)
	if err != nil {
		panic(fmt.Sprintf("snowflake init failed: %v", err))
	}

	var kp *kafka.Producer
	if len(c.Kafka.Brokers) > 0 {
		kp, err = kafka.NewProducer(c.Kafka, "audit.review.completed", logger)
		if err != nil {
			panic(fmt.Sprintf("kafka producer init failed: %v", err))
		}
	}

	var minioClient *minio.Client
	if c.MinIO.Endpoint != "" {
		minioClient, err = minio.NewClient(c.MinIO)
		if err != nil {
			panic(fmt.Sprintf("minio init failed: %v", err))
		}
		if err := minioClient.EnsureBucket(context.Background()); err != nil {
			panic(fmt.Sprintf("ensure minio bucket failed: %v", err))
		}
		if err := minioClient.SetPublicBucketPolicy(context.Background()); err != nil {
			panic(fmt.Sprintf("set minio bucket policy failed: %v", err))
		}
	}

	auditRepo := repo.NewAuditRepo(db)

	return &ServiceContext{
		Config:    c,
		DB:        db,
		AuditRepo: auditRepo,
		Snowflake: sf,
		KafkaProd: kp,
		MinIO:     minioClient,
		Logger:    logger,
	}
}
