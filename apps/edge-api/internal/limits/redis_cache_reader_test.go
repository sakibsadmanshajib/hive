package limits

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// =============================================================================
// The production read adapter, against a real Redis
//
// redisCacheReader is what stands between Redis and this gate on a deployed
// box, and until this file it was exercised by nothing: every gate test used the
// in-memory fake. Its one interesting behaviour is mapping redis.Nil to
// (ok=false, err=nil) rather than to an error, and getting that backwards is not
// a loud failure. It would make every cache miss an error, the gate would fail
// open on all of them, and the hard cap would stop being enforced silently:
// issue #1651 again, wearing a different hat.
// =============================================================================

func newReaderAgainstRedis(t *testing.T) (CacheReader, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisCacheReader(client), mr, client
}

func TestRedisCacheReader_MissIsNotAnError(t *testing.T) {
	reader, _, _ := newReaderAgainstRedis(t)

	val, ok, err := reader.Get(context.Background(), "budget:hard_cap:{absent}")
	if err != nil {
		t.Fatalf("a missing key reported an error: %v; the gate would fail open on every miss", err)
	}
	if ok {
		t.Fatalf("a missing key reported ok with value %q", val)
	}
}

func TestRedisCacheReader_ReadsTheStoredValue(t *testing.T) {
	reader, mr, _ := newReaderAgainstRedis(t)
	if err := mr.Set(contractMTDKey, contractMTDSubunits); err != nil {
		t.Fatalf("seed: %v", err)
	}

	val, ok, err := reader.Get(context.Background(), contractMTDKey)
	if err != nil || !ok {
		t.Fatalf("read of a present key: value=%q ok=%v err=%v", val, ok, err)
	}
	if val != contractMTDSubunits {
		t.Fatalf("read %q, want %q", val, contractMTDSubunits)
	}
}

// TestRedisCacheReader_ReportsARealFailure keeps the distinction the gate rests
// on: a miss is "no budget configured" and is not an error, while an unreachable
// Redis is an error, which is what drives the fail-open counter and its log line
// rather than being silently read as no spend.
func TestRedisCacheReader_ReportsARealFailure(t *testing.T) {
	reader, mr, _ := newReaderAgainstRedis(t)
	mr.Close()

	if _, _, err := reader.Get(context.Background(), contractHardCapKey); err == nil {
		t.Fatal("an unreachable Redis was reported as a clean miss, which the gate reads as no cap")
	}
}

// TestGateFailsOpenLoudlyOnAReaderError joins the two halves: the adapter
// reports the failure and the gate turns it into a served request plus a counted
// bypass, rather than a refusal or a silent pass.
func TestGateFailsOpenLoudlyOnAReaderError(t *testing.T) {
	reader, mr, _ := newReaderAgainstRedis(t)
	if err := mr.Set(contractHardCapKey, "50000"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mr.Close()

	bypasses := &recordingMetric{}
	gate, err := New(Config{
		Cache:                reader,
		WorkspaceFromRequest: resolverYes(),
		FailOpenMetric:       bypasses,
		Now:                  contractClock(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	served := false
	rec := httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if !served {
		t.Fatal("a Redis failure refused the request; the gate must fail open")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if bypasses.Calls() != 1 {
		t.Fatalf("fail-open counted %d times, want 1; an uncounted bypass is an unenforced cap nobody can see", bypasses.Calls())
	}
}
