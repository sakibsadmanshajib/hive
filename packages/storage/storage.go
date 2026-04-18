package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Config struct {
	Endpoint   string
	AccessKey  string
	SecretKey  string
	Region     string
	HTTPClient *http.Client
	Now        func() time.Time
}

type CompletePart struct {
	PartNumber int
	ETag       string
}

type Storage interface {
	Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, bucket, key string) error
	PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
	InitMultipartUpload(ctx context.Context, bucket, key, contentType string) (string, error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNum int, body io.Reader, size int64) (string, error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePart) error
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
}

type S3Client struct {
	cfg Config
}

func NewS3Client(cfg Config) (*S3Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("S3_ENDPOINT is required")
	}
	if cfg.AccessKey == "" {
		return nil, fmt.Errorf("S3_ACCESS_KEY is required")
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("S3_SECRET_KEY is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("S3_REGION is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &S3Client{cfg: cfg}, nil
}

func (s *S3Client) Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	return fmt.Errorf("storage implementation pending")
}

func (s *S3Client) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("storage implementation pending")
}

func (s *S3Client) Delete(ctx context.Context, bucket, key string) error {
	return fmt.Errorf("storage implementation pending")
}

func (s *S3Client) PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return "", fmt.Errorf("storage implementation pending")
}

func (s *S3Client) InitMultipartUpload(ctx context.Context, bucket, key, contentType string) (string, error) {
	return "", fmt.Errorf("storage implementation pending")
}

func (s *S3Client) UploadPart(ctx context.Context, bucket, key, uploadID string, partNum int, body io.Reader, size int64) (string, error) {
	return "", fmt.Errorf("storage implementation pending")
}

func (s *S3Client) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePart) error {
	return fmt.Errorf("storage implementation pending")
}

func (s *S3Client) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	return fmt.Errorf("storage implementation pending")
}
