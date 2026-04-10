package batches

import (
	"context"
	"io"

	"github.com/hivegpt/hive/apps/edge-api/internal/files"
)

// StorageAdapter adapts *files.StorageClient to the batches.StorageBackend interface.
type StorageAdapter struct {
	inner *files.StorageClient
}

// NewStorageAdapter wraps a StorageClient for use with the batches Handler.
func NewStorageAdapter(inner *files.StorageClient) *StorageAdapter {
	return &StorageAdapter{inner: inner}
}

// Download retrieves file content from blob storage.
func (a *StorageAdapter) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return a.inner.Download(ctx, bucket, key)
}
