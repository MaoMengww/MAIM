package minio

import (
	"bytes"
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
}

func TestNewClientEmptyCredentials(t *testing.T) {
	cfg := config.MinIOConfig{Endpoint: "localhost:9000"}
	client, err := NewClient(cfg)
	assert.Error(t, err)
	assert.Nil(t, client)
}

func TestUploadEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.Upload(t.Context(), "", bytes.NewReader(nil), 0, "text/plain")
	assert.Error(t, err)
}

func TestDownloadEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.Download(t.Context(), "")
	assert.Error(t, err)
}

func TestDeleteEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	err := client.Delete(t.Context(), "")
	assert.Error(t, err)
}

func TestPresignedURLEmptyName(t *testing.T) {
	cfg := config.MinIOConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
	}
	client, _ := NewClient(cfg)
	_, err := client.PresignedURL(t.Context(), "", time.Hour)
	assert.Error(t, err)
}

func TestPresignedURLsUseSeparateEndpoints(t *testing.T) {
	client, err := NewClient(config.MinIOConfig{
		Endpoint:       "minio:9000",
		PublicEndpoint: "127.0.0.1:32769",
		AccessKey:      "minioadmin",
		SecretKey:      "minioadmin",
		Region:         "us-east-1",
	})
	require.NoError(t, err)

	browserURL, err := client.PresignedURL(t.Context(), "files/input.csv", time.Minute)
	require.NoError(t, err)
	assert.Contains(t, browserURL, "127.0.0.1:32769")

	internalURL, err := client.InternalPresignedURL(t.Context(), "files/input.csv", time.Minute)
	require.NoError(t, err)
	assert.Contains(t, internalURL, "minio:9000")
}
