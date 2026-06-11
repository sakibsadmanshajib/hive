package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/db"
)

// TestOpen_EmptyURL verifies that an empty database URL is rejected immediately
// with a descriptive error before any network call is attempted.
func TestOpen_EmptyURL(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, "")
	if err == nil {
		pool.Close()
		t.Fatal("expected error for empty database URL, got nil")
	}
	if !strings.Contains(err.Error(), "database URL is empty") {
		t.Fatalf("expected 'database URL is empty' in error, got: %v", err)
	}
}

// TestOpen_InvalidURL verifies that a syntactically invalid DSN returns a
// wrapped error and does not return a non-nil pool.
func TestOpen_InvalidURL(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, "not-a-postgres-url")
	if err == nil {
		pool.Close()
		t.Fatal("expected error for invalid DSN, got nil")
	}
	if pool != nil {
		pool.Close()
		t.Fatal("expected nil pool on error")
	}
}

// TestOpen_UnreachableHost verifies that a well-formed DSN pointing at an
// unreachable host returns a non-nil error with pool closed (fail-closed
// semantics: we do not hand back a pool that cannot be pinged).
func TestOpen_UnreachableHost(t *testing.T) {
	ctx := context.Background()
	// Use a valid DSN format but an address that will always be refused.
	pool, err := db.Open(ctx, "postgres://user:pass@127.0.0.1:1/testdb?connect_timeout=1")
	if err == nil {
		pool.Close()
		t.Fatal("expected error for unreachable host, got nil")
	}
	if pool != nil {
		pool.Close()
		t.Fatal("expected nil pool when ping fails")
	}
}

// TestOpen_ErrorWrapping verifies that errors returned from Open contain
// contextual wrappers (i.e. the caller can identify the failure stage).
func TestOpen_ErrorWrapping(t *testing.T) {
	cases := []struct {
		name    string
		dsn     string
		wantMsg string
	}{
		{
			name:    "empty URL",
			dsn:     "",
			wantMsg: "database URL is empty",
		},
		{
			name:    "unreachable host",
			dsn:     "postgres://u:p@127.0.0.1:1/db?connect_timeout=1",
			wantMsg: "failed to",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool, err := db.Open(ctx, tc.dsn)
			if err == nil {
				pool.Close()
				t.Fatalf("expected error for DSN %q, got nil", tc.dsn)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}
