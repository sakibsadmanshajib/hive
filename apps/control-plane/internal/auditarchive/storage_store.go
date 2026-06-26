package auditarchive

import (
	"bytes"
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
	client  storage.Storage
	bucket  string
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

// Put writes r to key in the configured bucket and returns the number of bytes
// written. The caller verifies this equals the expected compressed size before
// deleting source rows (P1 truncation guard).
func (s *StorageObjectStore) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	// Buffer the reader to get an exact byte count for the size argument and
	// the post-write verification. Gzip archive payloads are small (<100 MB).
	// ponytail: in-memory buffer; switch to streaming multipart upload if payload sizes grow.
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("auditarchive storage: read %s: %w", key, err)
	}
	size := int64(len(data))
	if err := s.client.Upload(ctx, s.bucket, key, bytes.NewReader(data), size, "application/gzip"); err != nil {
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
