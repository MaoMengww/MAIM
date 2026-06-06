package svc

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/config"
	"github.com/maomeng/aim/app/file-service/internal/model"
	"github.com/maomeng/aim/app/file-service/internal/repo"
	"github.com/maomeng/aim/pkg/database"
	minioclient "github.com/maomeng/aim/pkg/minio"
	"github.com/maomeng/aim/pkg/snowflake"
)

type ServiceContext struct {
	Config    config.Config
	DB        *database.DB
	MinIO     MinIOClient
	Snowflake *snowflake.Node
	FileRepo  repo.FileRepoInterface
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := database.NewDB(c.Database, nil)
	if err != nil {
		panic("failed to init database: " + err.Error())
	}
	if err := db.AutoMigrate(&model.File{}); err != nil {
		panic("auto migrate failed: " + err.Error())
	}

	minioCli, err := minioclient.NewClient(c.MinIO)
	if err != nil {
		panic("failed to init minio: " + err.Error())
	}

	sn, err := snowflake.NewNode(5)
	if err != nil {
		panic("failed to init snowflake: " + err.Error())
	}

	if err := minioCli.EnsureBucket(context.Background()); err != nil {
		panic("failed to ensure minio bucket: " + err.Error())
	}
	if err := minioCli.SetPublicBucketPolicy(context.Background()); err != nil {
		panic("failed to set minio bucket policy: " + err.Error())
	}

	return &ServiceContext{
		Config:    c,
		DB:        db,
		MinIO:     newMinioAdapter(minioCli),
		Snowflake: sn,
		FileRepo:  repo.NewFileRepo(db),
	}
}
