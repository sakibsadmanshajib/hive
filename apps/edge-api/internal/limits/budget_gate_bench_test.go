package limits

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// BenchmarkBudgetGate exercises the full middleware path on a representative
// request: workspace resolution → fakeCache hard-cap read → MTD read → big.Int
// comparison → next-handler pass-through.
//
// Target: <2ms p99 added latency per Phase 14 PLAN. The fake cache simulates
// Redis with no network — the bench measures the Go-side overhead of the
// gate (math/big alloc, JSON-free path, http handler wrap). Real Redis adds
// ~0.1-0.5ms in cluster and dominates the wall-clock cost.
func BenchmarkBudgetGate(b *testing.B) {
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "100000")
	cache.set(MTDSpendRedisKeyPattern(testWorkspace, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), "50000")

	gate, err := New(Config{
		Cache:                cache,
		WorkspaceFromRequest: resolverYes(),
		Now:                  fixedClock(),
	})
	if err != nil {
		b.Fatal(err)
	}

	handler := gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status=%d", rec.Code)
		}
	}
}

// BenchmarkBudgetGate_Block exercises the 402 path so the bench captures the
// JSON-encoding tail.
func BenchmarkBudgetGate_Block(b *testing.B) {
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "1000")
	cache.set(MTDSpendRedisKeyPattern(testWorkspace, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), "1000")

	gate, err := New(Config{
		Cache:                cache,
		WorkspaceFromRequest: resolverYes(),
		Now:                  fixedClock(),
	})
	if err != nil {
		b.Fatal(err)
	}

	handler := gate.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusPaymentRequired {
			b.Fatalf("status=%d", rec.Code)
		}
	}
}
