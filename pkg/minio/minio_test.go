package minio

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientEmptyEndpoint(t *testing.T) {
	cfg := config.MinIOConfig{Endpoint: ""}
	client, err := NewClient(cfg)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestNewClientEmptyCredentials(t *testing.T) {
	cfg := config.MinIOConfig{Endpoint: "localhost:9000"}
	client, err := NewClient(cfg)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "access key")
}

func TestNewClientDefaultBucket(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, err := NewClient(cfg)
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, "aim", client.Bucket())
}

func TestNewClientCustomBucket(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		Bucket:    "custom-bucket",
	}
	client, err := NewClient(cfg)
	assert.NoError(t, err)
	assert.Equal(t, "custom-bucket", client.Bucket())
}

func TestUploadEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.Upload(context.Background(), "", bytes.NewReader(nil), 0, "text/plain")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "object name is required")
}

func TestDownloadEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.Download(context.Background(), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "object name is required")
}

func TestDeleteEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	err := client.Delete(context.Background(), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "object name is required")
}

func TestPresignedURLEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.PresignedURL(context.Background(), "", time.Hour)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "object name is required")
}

func TestPresignedURLDefaultExpiry(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.PresignedURL(context.Background(), "test.txt", 0)
	assert.Error(t, err)
}

func TestPresignedPutURL(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.PresignedPutURL(context.Background(), "upload.txt", time.Hour)
	assert.Error(t, err)
}

func TestUploadToServer(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)

	data := []byte("hello minio")
	_, err = client.Upload(context.Background(), "test/hello.txt", bytes.NewReader(data), int64(len(data)), "text/plain")
	assert.Error(t, err)
}

func TestListObjects(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	ch := client.ListObjects(context.Background(), "test/", true)
	assert.NotNil(t, ch)
}

func TestDownloadFromServer(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	reader, err := client.Download(context.Background(), "test/missing.txt")
	if err != nil {
		assert.Error(t, err)
		assert.Nil(t, reader)
		return
	}
	defer reader.Close()
	_, err = io.ReadAll(reader)
	assert.Error(t, err)
}

func TestNewClientWithSSL(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		UseSSL:    true,
	}
	client, err := NewClient(cfg)
	assert.NoError(t, err)
	assert.NotNil(t, client)
}
