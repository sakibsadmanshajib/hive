package storage

import (
	"context"
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
