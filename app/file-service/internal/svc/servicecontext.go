package svc

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/config"
	"github.com/maomeng/aim/app/file-service/internal/repo"
	"github.com/maomeng/aim/migrations/postgres"
	"github.com/maomeng/aim/pkg/database"
	minioclient "github.com/maomeng/aim/pkg/minio"
)

type ServiceContext struct {
	Config   config.Config
	DB       *database.DB
	MinIO    MinIOClient
	FileRepo repo.FileRepoInterface
}

func NewServiceContext(c config.Config) *ServiceContext {
	db, err := database.NewDB(c.Database, nil)
	if err != nil {
		panic("failed to init database: " + err.Error())
	}
	if err := database.RunMigrations(db.DB, postgres.FS); err != nil {
		panic("run migrations failed: " + err.Error())
	}

	minioCli, err := minioclient.NewClient(c.MinIO)
	if err != nil {
		panic("failed to init minio: " + err.Error())
	}

	if err := minioCli.EnsureBucket(context.Background()); err != nil {
		panic("failed to ensure minio bucket: " + err.Error())
	}
	if err := minioCli.SetPublicBucketPolicy(context.Background()); err != nil {
		panic("failed to set minio bucket policy: " + err.Error())
	}

	return &ServiceContext{
		Config:   c,
		DB:       db,
		MinIO:    newMinioAdapter(minioCli),
		FileRepo: repo.NewFileRepo(db),
	}
}
