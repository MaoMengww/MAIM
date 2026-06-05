package svc

import (
	"context"
	"io"
	"time"

	minioclient "github.com/maomeng/aim/pkg/minio"
)

type minioAdapter struct {
	cli *minioclient.Client
}

func newMinioAdapter(cli *minioclient.Client) MinIOClient {
	return &minioAdapter{cli: cli}
}

func (a *minioAdapter) Bucket() string { return a.cli.Bucket() }

func (a *minioAdapter) EnsureBucket(ctx context.Context) error { return a.cli.EnsureBucket(ctx) }

func (a *minioAdapter) SetPublicBucketPolicy(ctx context.Context) error {
	return a.cli.SetPublicBucketPolicy(ctx)
}

func (a *minioAdapter) PublicURL(objectName string) string { return a.cli.PublicURL(objectName) }

func (a *minioAdapter) Upload(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) (*UploadInfo, error) {
	info, err := a.cli.Upload(ctx, objectName, reader, size, contentType)
	if err != nil {
		return nil, err
	}
	return &UploadInfo{Size: info.Size, Key: info.Key}, nil
}

func (a *minioAdapter) Delete(ctx context.Context, objectName string) error {
	return a.cli.Delete(ctx, objectName)
}

func (a *minioAdapter) PresignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	return a.cli.PresignedURL(ctx, objectName, expiry)
}

func (a *minioAdapter) PresignedPutURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	return a.cli.PresignedPutURL(ctx, objectName, expiry)
}
