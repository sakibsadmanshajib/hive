package batches

import (
	"context"

	"github.com/hivegpt/hive/apps/edge-api/internal/files"
)

// FilestoreAdapter adapts *files.FilestoreClient to the batches.FileClientBackend interface.
type FilestoreAdapter struct {
	inner *files.FilestoreClient
}

// NewFilestoreAdapter wraps a FilestoreClient for use with the batches Handler.
func NewFilestoreAdapter(inner *files.FilestoreClient) *FilestoreAdapter {
	return &FilestoreAdapter{inner: inner}
}

// GetFile retrieves file metadata and maps it to the batches.FileObject type.
func (a *FilestoreAdapter) GetFile(ctx context.Context, id, accountID string) (*FileObject, error) {
	f, err := a.inner.GetFile(ctx, id, accountID)
	if err != nil {
		if err == files.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &FileObject{
		ID:          f.ID,
		StoragePath: f.StoragePath,
	}, nil
}
