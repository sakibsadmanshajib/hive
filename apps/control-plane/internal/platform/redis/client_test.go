package redis_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	platformredis "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/redis"
)

// TestNewClient_RedisURL verifies that a redis:// URL is parsed and the
// resulting client can reach a live miniredis server.
func TestNewClient_RedisURL(t *testing.T) {
	mr := miniredis.RunT(t)

	client := platformredis.NewClient("redis://" + mr.Addr())
	if client == nil {
		t.Fatal("expected non-nil client from redis:// URL")
	}
	defer client.Close()

	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping after NewClient(redis:// URL) failed: %v", err)
	}
}

// TestNewClient_RawAddr verifies that a bare host:port address is accepted
// and the resulting client can reach a live miniredis server.
func TestNewClient_RawAddr(t *testing.T) {
	mr := miniredis.RunT(t)

	client := platformredis.NewClient(mr.Addr())
	if client == nil {
		t.Fatal("expected non-nil client from raw addr")
	}
	defer client.Close()

	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping after NewClient(raw addr) failed: %v", err)
	}
}

// TestNewClient_InvalidURL_FallsBackToRawAddr verifies that an unparseable
// URL does not panic and falls back to raw-addr semantics (non-nil client).
func TestNewClient_InvalidURL_FallsBackToRawAddr(t *testing.T) {
	client := platformredis.NewClient("not-a-valid-url://???")
	if client == nil {
		t.Fatal("expected non-nil client even for invalid URL (fallback to raw addr)")
	}
	defer client.Close()
}

// TestPing_Success verifies Ping returns nil against a healthy miniredis.
func TestPing_Success(t *testing.T) {
	mr := miniredis.RunT(t)

	client := platformredis.NewClient(mr.Addr())
	defer client.Close()

	if err := platformredis.Ping(context.Background(), client); err != nil {
		t.Fatalf("Ping returned unexpected error: %v", err)
	}
}

// TestPing_Failure verifies Ping propagates an error when the server is down
// (fail-closed semantics: an error is returned, not swallowed).
func TestPing_Failure(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	mr.Close() // shut down before ping

	client := platformredis.NewClient(addr)
	defer client.Close()

	err := platformredis.Ping(context.Background(), client)
	if err == nil {
		t.Fatal("expected error from Ping against closed server, got nil")
	}
}
