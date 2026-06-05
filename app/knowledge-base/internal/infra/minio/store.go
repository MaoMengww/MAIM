package minio

import (
	"context"
	"io"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	pkgminio "github.com/maomeng/aim/pkg/minio"
)

type FileStore struct {
	client *pkgminio.Client
}

func NewFileStore(client *pkgminio.Client) domain.FileStore {
	return &FileStore{client: client}
}

func (s *FileStore) Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	_, err := s.client.Upload(ctx, key, reader, size, contentType)
	return err
}

func (s *FileStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.client.Download(ctx, key)
}

func (s *FileStore) Delete(ctx context.Context, key string) error {
	return s.client.Delete(ctx, key)
}
