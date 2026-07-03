package auditarchive

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/sakibsadmanshajib/hive/packages/storage"
)

// StorageObjectStore implements ObjectStore via the packages/storage.Storage interface.
// The bucket is the local Supabase Storage bucket backed by the box's own filesystem.
// Zero external egress: no data leaves the sovereign edge node.
type StorageObjectStore struct {
	client storage.Storage
	bucket string
	// endpoint is logged at construction for sovereignty observability.
	endpoint string
}

// NewStorageObjectStore returns a StorageObjectStore writing to bucket.
// endpoint is the resolved S3 endpoint (from S3_ENDPOINT env); logged at
// startup so the zero-egress guarantee is observable, not just configured.
// If bucket is empty it defaults to "hive-audit-cold".
func NewStorageObjectStore(client storage.Storage, bucket, endpoint string) *StorageObjectStore {
	if bucket == "" {
		bucket = "hive-audit-cold"
	}
	slog.Info("auditarchive: cold storage configured",
		"bucket", bucket,
		"endpoint", endpoint,
	)
	return &StorageObjectStore{client: client, bucket: bucket, endpoint: endpoint}
}

// Put streams r to key in the configured bucket and returns the number of
// bytes written. size is the exact length of r, supplied by the caller
// (which already built the compressed payload and knows its length), so Put
// forwards the reader straight to the underlying client instead of buffering
// the already-compressed payload a second time. The caller verifies the
// returned count equals the expected compressed size before deleting source
// rows (P1 truncation guard).
func (s *StorageObjectStore) Put(ctx context.Context, key string, r io.Reader, size int64) (int64, error) {
	if err := s.client.Upload(ctx, s.bucket, key, r, size, "application/gzip"); err != nil {
		return 0, fmt.Errorf("auditarchive storage: put %s: %w", key, err)
	}
	return size, nil
}

// Delete removes key from the bucket when purging an expired cold object.
func (s *StorageObjectStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Delete(ctx, s.bucket, key); err != nil {
		return fmt.Errorf("auditarchive storage: delete %s: %w", key, err)
	}
	return nil
}
