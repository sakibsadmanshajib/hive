package batches

import (
	"context"
	"io"
)

type downloadBackend interface {
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// StorageAdapter adapts a storage downloader to the batches.StorageBackend interface.
type StorageAdapter struct {
	inner downloadBackend
}

// NewStorageAdapter wraps a storage downloader for use with the batches Handler.
func NewStorageAdapter(inner downloadBackend) *StorageAdapter {
	return &StorageAdapter{inner: inner}
}

// Download retrieves file content from blob storage.
func (a *StorageAdapter) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return a.inner.Download(ctx, bucket, key)
}
