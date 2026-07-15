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

func gatesOf(feats ...featuregate.Feature) featuregate.FlagsResponse {
	m := make(map[string]bool, len(feats))
	for _, f := range feats {
		m[string(f)] = true
	}
	return featuregate.FlagsResponse{Gates: m}
}

// ---- cache hit / miss / expiry ---------------------------------------------

func TestCacheHit(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: gatesOf(featuregate.FeatureRAG)}
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
	cp := &mockCP{flags: gatesOf(featuregate.FeatureRAG)}
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
			if !flags.Gates[string(featuregate.FeatureRAG)] {
				t.Error("expected shared result to have RAG gate enabled")
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
	if len(flags.Gates) != 0 {
		t.Error("no gate may be enabled (fail-closed) on upstream error")
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
	if len(flags.Gates) != 0 {
		t.Error("no gate may be enabled on network error")
	}
}

// ---- middleware: disabled -> 403, no feature name in body ------------------

func TestMiddleware_Disabled_Returns403(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{}}
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
	cp := &mockCP{flags: gatesOf(featuregate.FeatureVoice)}
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
	cases := []featuregate.Feature{
		featuregate.FeatureRAG,
		featuregate.FeatureVoice,
		featuregate.FeatureRelay,
		featuregate.FeatureCowork,
	}

	for _, feat := range cases {
		feat := feat
		t.Run(string(feat), func(t *testing.T) {
			cp := &mockCP{flags: gatesOf(feat)}
			srv := httptest.NewServer(cp)
			defer srv.Close()

			g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

			reached := false
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			g.Require(feat)(inner).ServeHTTP(rec, newRequest(uuid.New()))

			if !reached {
				t.Errorf("%s: inner handler not reached when enabled", feat)
			}
		})
	}
}

// TestRequire_NewGateKey_NoCodeChangeNeeded is the acceptance-check test
// (issue #293): a gate key the featuregate package has never declared a
// constant for still gates a route correctly. Adding a real gate costs one
// migration edit (the tenant_setting_key enum plus feature_gate_keys row);
// this package never needs a switch-case or struct-field update again.
func TestRequire_NewGateKey_NoCodeChangeNeeded(t *testing.T) {
	const featureExperimental featuregate.Feature = "ENABLE_EXPERIMENTAL_THING"

	tid := uuid.New()
	cp := &mockCP{flags: gatesOf(featureExperimental)}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	g.Require(featureExperimental)(inner).ServeHTTP(rec, newRequest(tid))

	if !reached {
		t.Error("a novel Feature value must gate correctly with zero package changes")
	}
}

// ---- RequireAny: OR-composition, used for SSO (any provider key) ----------

func TestRequireAny_EnabledWhenAnyKeySet(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: gatesOf(featuregate.FeatureSSOGoogle)}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	g.RequireAny(featuregate.FeatureSSOGoogle, featuregate.FeatureSSOMicrosoft, featuregate.FeatureSSOSaml)(inner).
		ServeHTTP(rec, newRequest(tid))

	if !reached {
		t.Error("inner handler not reached when one of the OR'd keys is enabled")
	}
}

func TestRequireAny_DisabledWhenNoneSet(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	g.RequireAny(featuregate.FeatureSSOGoogle, featuregate.FeatureSSOMicrosoft, featuregate.FeatureSSOSaml)(inner).
		ServeHTTP(rec, newRequest(tid))

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when none of the OR'd keys is set, got %d", rec.Code)
	}
	// Provider-blind: body must not leak any key name.
	body := rec.Body.String()
	for _, banned := range []string{"sso", "SSO", "saml", "SAML", "oidc", "OIDC", "google", "microsoft", "feature"} {
		if strings.Contains(body, banned) {
			t.Errorf("response body leaks %q: %s", banned, body)
		}
	}
}

func TestFetch_GatesMap_Propagated(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: gatesOf(featuregate.FeatureSSOSaml)}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})
	flags, err := g.Fetch(newRequest(tid).Context(), tid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !flags.Gates[string(featuregate.FeatureSSOSaml)] {
		t.Error("Gates map must be propagated verbatim from control-plane response")
	}
}

func TestFetch_GatesMap_EmptyOnZeroResponse(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: featuregate.FlagsResponse{}} // all false
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})
	flags, err := g.Fetch(newRequest(tid).Context(), tid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(flags.Gates) != 0 {
		t.Error("Gates must be empty/false by default")
	}
}
