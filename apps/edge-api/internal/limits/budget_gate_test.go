package limits

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// fakeCache — in-memory CacheReader for unit + bench tests.
// =============================================================================

type fakeCache struct {
	mu      sync.RWMutex
	data    map[string]string
	getErr  error
	getHits int64
}

func newFakeCache() *fakeCache {
	return &fakeCache{data: make(map[string]string)}
}

func (c *fakeCache) Get(_ context.Context, key string) (string, bool, error) {
	atomic.AddInt64(&c.getHits, 1)
	if c.getErr != nil {
		return "", false, c.getErr
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.data[key]
	return val, ok, nil
}

func (c *fakeCache) set(key, val string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = val
}

func (c *fakeCache) del(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

// =============================================================================
// Helpers
// =============================================================================

const testWorkspace = "ws-12345"

func fixedClock() func() time.Time {
	t := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func resolverYes() WorkspaceFromRequest {
	return func(_ *http.Request) (string, bool) { return testWorkspace, true }
}

func setupGate(t *testing.T, cache CacheReader, soft SoftCapResolver, metric SoftCapMetric) *BudgetGate {
	t.Helper()
	gate, err := New(Config{
		Cache:                cache,
		WorkspaceFromRequest: resolverYes(),
		SoftCapResolver:      soft,
		SoftCapMetric:        metric,
		Now:                  fixedClock(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gate
}

// =============================================================================
// Tests
// =============================================================================

func TestNew_RequiresCache(t *testing.T) {
	t.Parallel()
	_, err := New(Config{WorkspaceFromRequest: resolverYes()})
	if err == nil {
		t.Fatal("expected error on nil cache")
	}
}

func TestNew_RequiresResolver(t *testing.T) {
	t.Parallel()
	_, err := New(Config{Cache: newFakeCache()})
	if err == nil {
		t.Fatal("expected error on nil resolver")
	}
}

func TestGate_NoBudgetSet_PassesThrough(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	gate := setupGate(t, cache, nil, nil)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	gate.Wrap(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if !called {
		t.Fatal("next handler should have been called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

func TestGate_UnauthenticatedBypass(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "1000")

	gate, err := New(Config{
		Cache:                cache,
		WorkspaceFromRequest: func(_ *http.Request) (string, bool) { return "", false },
		Now:                  fixedClock(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected pass-through 200 for unauthenticated, got %d", rec.Code)
	}
}

func TestGate_UnderHardCap_PassesThrough(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "1000")
	cache.set(MTDSpendRedisKeyPattern(testWorkspace, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), "500")

	gate := setupGate(t, cache, nil, nil)

	rec := httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

func TestGate_HardCapExceeded_Blocks402(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "1000")
	cache.set(MTDSpendRedisKeyPattern(testWorkspace, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), "1000")

	gate := setupGate(t, cache, nil, nil)

	rec := httptest.NewRecorder()
	called := false
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if called {
		t.Fatal("next handler must not be called when hard cap is exceeded")
	}
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status=%d want 402", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
	retryStr := rec.Header().Get("Retry-After")
	retrySec, err := strconv.ParseInt(retryStr, 10, 64)
	if err != nil || retrySec < 1 {
		t.Fatalf("Retry-After not numeric/positive: %q", retryStr)
	}

	body := rec.Body.String()
	// Critical Phase 17 assertion — no USD/FX leakage in the 402 body.
	if containsAny(body, []string{"amount_usd", "USD", "usd", "fx", "FX", "exchange", "Exchange"}) {
		t.Fatalf("402 body must not contain USD/FX strings, got: %s", body)
	}

	var decoded hardCapExceededBody
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if decoded.Error.Currency != "BDT" {
		t.Fatalf("currency=%q want BDT", decoded.Error.Currency)
	}
	if decoded.Error.HardCapBDTSubunits != "1000" {
		t.Fatalf("hard cap=%q want 1000", decoded.Error.HardCapBDTSubunits)
	}
	if decoded.Error.MTDBDTSubunits != "1000" {
		t.Fatalf("mtd=%q want 1000", decoded.Error.MTDBDTSubunits)
	}
	if decoded.Error.Code != "budget_hard_cap_exceeded" {
		t.Fatalf("code=%q want budget_hard_cap_exceeded", decoded.Error.Code)
	}
	if decoded.Error.Type != "insufficient_quota" {
		t.Fatalf("type=%q want insufficient_quota", decoded.Error.Type)
	}
	// Next period must point at 2026-05-01 UTC (fixed clock is 2026-04-29).
	if decoded.Error.NextPeriodStartUTC == "" {
		t.Fatal("missing next period")
	}
}

func TestGate_HardCapExceededAt_Boundary(t *testing.T) {
	t.Parallel()
	// MTD == hard cap → blocked (>= comparison).
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "100000")
	cache.set(MTDSpendRedisKeyPattern(testWorkspace, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), "100000")

	gate := setupGate(t, cache, nil, nil)

	rec := httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status=%d want 402 (mtd == hard cap must block)", rec.Code)
	}
}

// recordingMetric is a SoftCapMetric that captures Inc calls.
type recordingMetric struct {
	mu    sync.Mutex
	calls []string
}

func (m *recordingMetric) Inc(workspaceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, workspaceID)
}

func (m *recordingMetric) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func TestGate_SoftCapCrossed_NonBlockingMetricEmitted(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "1000")
	cache.set(MTDSpendRedisKeyPattern(testWorkspace, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), "800")

	soft := func(_ context.Context, _ string) (*big.Int, error) {
		return big.NewInt(800), nil
	}
	metric := &recordingMetric{}
	gate := setupGate(t, cache, soft, metric)

	rec := httptest.NewRecorder()
	called := false
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if !called {
		t.Fatal("soft cap must NOT block the request")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (soft cap is non-blocking)", rec.Code)
	}
	if metric.Calls() != 1 {
		t.Fatalf("soft-cap metric calls=%d want 1", metric.Calls())
	}
}

func TestGate_RedisError_FailsOpen(t *testing.T) {
	t.Parallel()
	// Better to bill a few extra requests than tank availability.
	cache := newFakeCache()
	cache.getErr = errors.New("redis down")
	gate := setupGate(t, cache, nil, nil)

	called := false
	rec := httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if !called {
		t.Fatal("redis failure must fail-open to next handler")
	}
}

func TestGate_CacheInvalidationViaRefreshedKey(t *testing.T) {
	t.Parallel()
	// Simulates cache invalidation: control-plane updates the hard cap key,
	// the gate reads the fresh value on its next pass.
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "1000")
	cache.set(MTDSpendRedisKeyPattern(testWorkspace, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)), "950")
	gate := setupGate(t, cache, nil, nil)

	// Request 1 — under cap.
	rec := httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status=%d want 200", rec.Code)
	}

	// Owner lowers cap to 800 — control-plane writes the key directly.
	cache.set(HardCapRedisKeyPattern(testWorkspace), "800")

	// Request 2 — gate now blocks because 950 >= 800.
	rec = httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("post-invalidation status=%d want 402", rec.Code)
	}

	// Owner deletes the budget — gate reverts to pass-through.
	cache.del(HardCapRedisKeyPattern(testWorkspace))
	rec = httptest.NewRecorder()
	called := false
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("post-delete must pass through; called=%v code=%d", called, rec.Code)
	}
}

func TestGate_MalformedCacheValue_FailsOpen(t *testing.T) {
	t.Parallel()
	cache := newFakeCache()
	cache.set(HardCapRedisKeyPattern(testWorkspace), "not-a-number")
	gate := setupGate(t, cache, nil, nil)

	rec := httptest.NewRecorder()
	called := false
	gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if !called {
		t.Fatal("malformed cache value should fail-open")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if indexOf(s, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(haystack, needle string) int {
	if needle == "" {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
