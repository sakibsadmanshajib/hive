package invoices

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/hivegpt/hive/packages/storage"
)

// ErrStorageBackend is the sanitized sentinel surfaced across the service
// boundary on any object-storage failure. The original error is logged
// (via slog) for debug, but never returned upstream verbatim — that prevents
// provider / bucket internals leaking onto a customer-visible response.
var ErrStorageBackend = errors.New("invoices: storage backend unavailable")

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
	if err := a.inner.Upload(ctx, bucket, key, asReader{body}, size, contentType); err != nil {
		slog.WarnContext(ctx, "invoices: storage upload failed",
			"bucket", bucket, "key", key, "error", err)
		return ErrStorageBackend
	}
	return nil
}

func (a *storageAdapter) PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	url, err := a.inner.PresignedURL(ctx, bucket, key, ttl)
	if err != nil {
		slog.WarnContext(ctx, "invoices: storage presign failed",
			"bucket", bucket, "key", key, "error", err)
		return "", ErrStorageBackend
	}
	return url, nil
}

// asReader lifts a bytesReader to io.Reader (a method-set widening; the body
// is already implementing Read with the same signature).
type asReader struct {
	r bytesReader
}

func (a asReader) Read(p []byte) (int, error) { return a.r.Read(p) }

// Compile-time guard: asReader implements io.Reader.
var _ io.Reader = asReader{}
