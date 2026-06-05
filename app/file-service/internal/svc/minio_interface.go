package svc

import (
	"context"
	"io"
	"time"
)

type UploadInfo struct {
	Size int64
	Key  string
}

type MinIOClient interface {
	Bucket() string
	EnsureBucket(ctx context.Context) error
	SetPublicBucketPolicy(ctx context.Context) error
	Upload(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) (*UploadInfo, error)
	Delete(ctx context.Context, objectName string) error
	PublicURL(objectName string) string
	PresignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error)
	PresignedPutURL(ctx context.Context, objectName string, expiry time.Duration) (string, error)
}
