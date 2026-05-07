package invoices

import (
	"context"
	"io"
	"time"

	"github.com/hivegpt/hive/packages/storage"
)

// =============================================================================
// Storage adapter — bridges packages/storage.Storage (io.Reader) to the local
// storageBackend interface (whose Read signature is the minimal subset
// required by gofpdf-rendered PDF uploads).
// =============================================================================

// NewStorageAdapter wraps a packages/storage.Storage as the local
// storageBackend the invoice service consumes.
func NewStorageAdapter(s storage.Storage) storageBackend {
	return &storageAdapter{inner: s}
}

type storageAdapter struct {
	inner storage.Storage
}

func (a *storageAdapter) Upload(
	ctx context.Context,
	bucket, key string,
	body bytesReader,
	size int64,
	contentType string,
) error {
	return a.inner.Upload(ctx, bucket, key, asReader{body}, size, contentType)
}

func (a *storageAdapter) PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return a.inner.PresignedURL(ctx, bucket, key, ttl)
}

// asReader lifts a bytesReader to io.Reader (a method-set widening; the body
// is already implementing Read with the same signature).
type asReader struct {
	r bytesReader
}

func (a asReader) Read(p []byte) (int, error) { return a.r.Read(p) }

// Compile-time guard: asReader implements io.Reader.
var _ io.Reader = asReader{}
