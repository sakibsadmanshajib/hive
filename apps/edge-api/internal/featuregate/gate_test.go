// Package featuregate tests — TDD RED first.
package featuregate_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/featuregate"
)

// ---- helpers ---------------------------------------------------------------

// mockCP serves a fixed FlagsResponse for all tenants.
type mockCP struct {
	flags      featuregate.FlagsResponse
	calls      atomic.Int32
	statusCode int           // overrides 200 when non-zero
	delay      time.Duration // artificial upstream latency, for stampede tests
}

func (m *mockCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.calls.Add(1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	code := http.StatusOK
	if m.statusCode != 0 {
		code = m.statusCode
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if code == http.StatusOK {
		_ = json.NewEncoder(w).Encode(m.flags)
	}
}

func newRequest(tenantID uuid.UUID) *http.Request {
	u := &auth.User{TenantID: tenantID}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	return r.WithContext(auth.WithUser(r.Context(), u))
}

// ---- cache hit / miss / expiry ---------------------------------------------

func TestCacheHit(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{RAGEnabled: true}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	for i := 0; i < 2; i++ {
		if _, err := g.Fetch(newRequest(tid).Context(), tid); err != nil {
			t.Fatalf("Fetch call %d: %v", i, err)
		}
	}
	if got := cp.calls.Load(); got != 1 {
		t.Errorf("expected 1 upstream call (cache hit on 2nd), got %d", got)
	}
}

func TestCacheMiss_MultiTenant(t *testing.T) {
	t1, t2 := uuid.New(), uuid.New()
	cp := &mockCP{}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	_, _ = g.Fetch(newRequest(t1).Context(), t1)
	_, _ = g.Fetch(newRequest(t2).Context(), t2)
	if got := cp.calls.Load(); got != 2 {
		t.Errorf("expected 2 upstream calls (distinct tenants), got %d", got)
	}
}

func TestCacheExpiry(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 1 * time.Millisecond})

	_, _ = g.Fetch(newRequest(tid).Context(), tid)
	time.Sleep(5 * time.Millisecond)
	_, _ = g.Fetch(newRequest(tid).Context(), tid)

	if got := cp.calls.Load(); got != 2 {
		t.Errorf("expected 2 upstream calls after TTL expiry, got %d", got)
	}
}

// ---- cache stampede: concurrent miss dedup (issue #253) --------------------

// TestFetch_ConcurrentMiss_SingleUpstreamCall reproduces the cold-cache
// stampede: N goroutines call Fetch for the same tenant before the first
// upstream response returns. Only one upstream request should fire; the rest
// must wait for and share that result (singleflight).
func TestFetch_ConcurrentMiss_SingleUpstreamCall(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{RAGEnabled: true}}
	cp.delay = 50 * time.Millisecond
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			flags, err := g.Fetch(newRequest(tid).Context(), tid)
			if err != nil {
				t.Errorf("Fetch: %v", err)
				return
			}
			if !flags.RAGEnabled {
				t.Error("expected shared result to have RAGEnabled=true")
			}
		}()
	}
	wg.Wait()

	if got := cp.calls.Load(); got != 1 {
		t.Errorf("expected 1 upstream call under concurrent cold-cache miss, got %d", got)
	}
}

// ---- fail-closed on upstream errors ----------------------------------------

func TestFetch_FailsClosed_On500(t *testing.T) {
	cp := &mockCP{statusCode: http.StatusInternalServerError}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	tid := uuid.New()
	flags, err := g.Fetch(newRequest(tid).Context(), tid)
	if err == nil {
		t.Fatal("expected error from 500 response, got nil")
	}
	if flags.RAGEnabled || flags.VoiceEnabled || flags.RelayEnabled || flags.CoworkEnabled {
		t.Error("all flags must be false (fail-closed) on upstream error")
	}
}

func TestFetch_FailsClosed_OnNetworkError(t *testing.T) {
	g := featuregate.New(featuregate.Config{
		ControlPlaneURL: "http://127.0.0.1:1", // connection refused
		TTL:             30 * time.Second,
	})

	tid := uuid.New()
	flags, err := g.Fetch(newRequest(tid).Context(), tid)
	if err == nil {
		t.Fatal("expected error on network failure, got nil")
	}
	if flags.RAGEnabled || flags.VoiceEnabled || flags.RelayEnabled || flags.CoworkEnabled {
		t.Error("all flags must be false on network error")
	}
}

// ---- middleware: disabled → 403, no feature name in body ------------------

func TestMiddleware_Disabled_Returns403(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{RAGEnabled: false}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := g.Require(featuregate.FeatureRAG)(inner)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest(tid))

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
	// Provider-blind: no feature name in body.
	body := rec.Body.String()
	for _, banned := range []string{"rag", "RAG", "voice", "relay", "cowork", "feature"} {
		if strings.Contains(body, banned) {
			t.Errorf("response body leaks %q in: %s", banned, body)
		}
	}
}

func TestMiddleware_Enabled_PassesThrough(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{VoiceEnabled: true}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	g.Require(featuregate.FeatureVoice)(inner).ServeHTTP(rec, newRequest(tid))

	if !reached {
		t.Error("inner handler not reached for enabled feature")
	}
}

func TestMiddleware_NoUser_Returns403(t *testing.T) {
	cp := &mockCP{}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil) // no auth.User in context
	rec := httptest.NewRecorder()
	g.Require(featuregate.FeatureRAG)(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unauthenticated request, got %d", rec.Code)
	}
}

// ---- all four feature constants covered ------------------------------------

func TestMiddleware_AllFeatures_Enabled(t *testing.T) {
	cases := []struct {
		feat  featuregate.Feature
		setup func(*featuregate.FlagsResponse)
	}{
		{featuregate.FeatureRAG, func(f *featuregate.FlagsResponse) { f.RAGEnabled = true }},
		{featuregate.FeatureVoice, func(f *featuregate.FlagsResponse) { f.VoiceEnabled = true }},
		{featuregate.FeatureRelay, func(f *featuregate.FlagsResponse) { f.RelayEnabled = true }},
		{featuregate.FeatureCowork, func(f *featuregate.FlagsResponse) { f.CoworkEnabled = true }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.feat), func(t *testing.T) {
			var flags featuregate.FlagsResponse
			tc.setup(&flags)
			cp := &mockCP{flags: flags}
			srv := httptest.NewServer(cp)
			defer srv.Close()

			g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

			reached := false
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			g.Require(tc.feat)(inner).ServeHTTP(rec, newRequest(uuid.New()))

			if !reached {
				t.Errorf("%s: inner handler not reached when enabled", tc.feat)
			}
		})
	}
}

// ---- SSO feature gate (issue #237) -----------------------------------------

func TestMiddleware_SSO_Enabled_PassesThrough(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{SSOEnabled: true}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	g.Require(featuregate.FeatureSSO)(inner).ServeHTTP(rec, newRequest(tid))

	if !reached {
		t.Error("inner handler not reached when SSO enabled")
	}
}

func TestMiddleware_SSO_Disabled_Returns403(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{SSOEnabled: false}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	g.Require(featuregate.FeatureSSO)(inner).ServeHTTP(rec, newRequest(tid))

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when SSO disabled, got %d", rec.Code)
	}
	// Provider-blind: body must not leak "sso" or "saml" or "oidc".
	body := rec.Body.String()
	for _, banned := range []string{"sso", "SSO", "saml", "SAML", "oidc", "OIDC", "feature"} {
		if strings.Contains(body, banned) {
			t.Errorf("response body leaks %q: %s", banned, body)
		}
	}
}

func TestFetch_SSOFlag_Propagated(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{SSOEnabled: true}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})
	flags, err := g.Fetch(newRequest(tid).Context(), tid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !flags.SSOEnabled {
		t.Error("SSOEnabled must be propagated from control-plane response")
	}
}

func TestFetch_SSOFlag_False_OnZeroResponse(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{}} // all false
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})
	flags, err := g.Fetch(newRequest(tid).Context(), tid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if flags.SSOEnabled {
		t.Error("SSOEnabled must default to false")
	}
}
