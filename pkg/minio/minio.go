package minio

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	miniocred "github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/maomeng/aim/pkg/config"
)

type Client struct {
	client         *minio.Client
	bucket         string
	publicEndpoint string
}

func NewClient(cfg config.MinIOConfig) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("minio endpoint is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("minio access key and secret key are required")
	}
	if cfg.Bucket == "" {
		cfg.Bucket = "aim"
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  miniocred.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client failed: %w", err)
	}

	return &Client{client: client, bucket: cfg.Bucket, publicEndpoint: cfg.PublicEndpoint}, nil
}

func (c *Client) Bucket() string {
	return c.bucket
}

func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.client.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("check bucket exists failed: %w", err)
	}
	if !exists {
		if err := c.client.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("create bucket failed: %w", err)
		}
	}
	return nil
}

func (c *Client) Upload(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) (*minio.UploadInfo, error) {
	if objectName == "" {
		return nil, fmt.Errorf("object name is required")
	}
	opts := minio.PutObjectOptions{ContentType: contentType}
	info, err := c.client.PutObject(ctx, c.bucket, objectName, reader, size, opts)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	return &info, nil
}

func (c *Client) Download(ctx context.Context, objectName string) (io.ReadCloser, error) {
	if objectName == "" {
		return nil, fmt.Errorf("object name is required")
	}
	obj, err := c.client.GetObject(ctx, c.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	return obj, nil
}

func (c *Client) Delete(ctx context.Context, objectName string) error {
	if objectName == "" {
		return fmt.Errorf("object name is required")
	}
	return c.client.RemoveObject(ctx, c.bucket, objectName, minio.RemoveObjectOptions{})
}

func (c *Client) PresignedURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	if objectName == "" {
		return "", fmt.Errorf("object name is required")
	}
	if expiry <= 0 {
		expiry = time.Hour
	}
	url, err := c.client.PresignedGetObject(ctx, c.bucket, objectName, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("generate presigned url failed: %w", err)
	}
	return c.replaceEndpoint(url.String()), nil
}

func (c *Client) PresignedPutURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	if objectName == "" {
		return "", fmt.Errorf("object name is required")
	}
	if expiry <= 0 {
		expiry = time.Hour
	}
	url, err := c.client.PresignedPutObject(ctx, c.bucket, objectName, expiry)
	if err != nil {
		return "", fmt.Errorf("generate presigned put url failed: %w", err)
	}
	return c.replaceEndpoint(url.String()), nil
}

func (c *Client) ListObjects(ctx context.Context, prefix string, recursive bool) <-chan minio.ObjectInfo {
	return c.client.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: recursive,
	})
}

// SetPublicBucketPolicy sets the bucket policy to allow public read access.
func (c *Client) SetPublicBucketPolicy(ctx context.Context) error {
	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Principal": {"AWS": ["*"]},
			"Action": ["s3:GetObject"],
			"Resource": ["arn:aws:s3:::%s/*"]
		}]
	}`, c.bucket)
	return c.client.SetBucketPolicy(ctx, c.bucket, policy)
}

// PublicURL returns the public accessible URL for the given object.
func (c *Client) PublicURL(objectName string) string {
	url := fmt.Sprintf("%s/%s/%s", c.client.EndpointURL(), c.bucket, objectName)
	return c.replaceEndpoint(url)
}

// replaceEndpoint substitutes the internal endpoint with the public endpoint
// in the given URL string, so that URLs returned to the browser use an
// externally-resolvable hostname.
func (c *Client) replaceEndpoint(rawURL string) string {
	if c.publicEndpoint == "" {
		return rawURL
	}
	internal := c.client.EndpointURL().String()
	return strings.Replace(rawURL, internal, withScheme(c.publicEndpoint, c.client.EndpointURL().Scheme), 1)
}

// withScheme ensures the endpoint string has a scheme prefix (http:// or https://).
func withScheme(endpoint, defaultScheme string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	if defaultScheme == "" {
		defaultScheme = "https"
	}
	return defaultScheme + "://" + endpoint
}
