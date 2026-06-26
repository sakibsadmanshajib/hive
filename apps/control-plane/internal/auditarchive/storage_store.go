package auditarchive

import (
	"context"
	"fmt"
	"io"

	"github.com/sakibsadmanshajib/hive/packages/storage"
)

// StorageObjectStore implements ObjectStore via the packages/storage.Storage interface.
// The bucket is the local Supabase Storage bucket backed by the box's own filesystem.
// Zero external egress: no data leaves the sovereign edge node.
type StorageObjectStore struct {
	client storage.Storage
	bucket string
}

// NewStorageObjectStore returns a StorageObjectStore writing to bucket.
// If bucket is empty it defaults to "hive-audit-cold".
func NewStorageObjectStore(client storage.Storage, bucket string) *StorageObjectStore {
	if bucket == "" {
		bucket = "hive-audit-cold"
	}
	return &StorageObjectStore{client: client, bucket: bucket}
}

// Put writes r to key in the configured bucket. The upload is write-once:
// callers must not call Put for a key that already exists in the manifest.
func (s *StorageObjectStore) Put(ctx context.Context, key string, r io.Reader) error {
	// Size -1 signals unknown length; the S3 client will use chunked transfer.
	// ponytail: size=-1 works for our gzip payloads (<100 MB); use multipart if sizes grow.
	if err := s.client.Upload(ctx, s.bucket, key, r, -1, "application/gzip"); err != nil {
		return fmt.Errorf("auditarchive storage: put %s: %w", key, err)
	}
	return nil
}

// Delete removes key from the bucket when purging an expired cold object.
func (s *StorageObjectStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Delete(ctx, s.bucket, key); err != nil {
		return fmt.Errorf("auditarchive storage: delete %s: %w", key, err)
	}
	return nil
}
