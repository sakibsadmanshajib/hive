package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// NewRouter builds the handler served on the public port, which the demo and
// cloud deployments publish through the ingress tunnel. The Prometheus series
// name upstream providers, count payment-rail events, and enumerate every
// /internal/* endpoint, so the scrape endpoint belongs on the separate
// telemetry listener in cmd/server and must never be registered here.
func TestNewRouterDoesNotServeMetrics(t *testing.T) {
	h := NewRouter(RouterConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics on the public router = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// Guards the test above against passing vacuously: an unwired router would 404
// on everything.
func TestNewRouterServesHealth(t *testing.T) {
	// ProvisioningReady is supplied because a nil reporter now degrades this
	// endpoint on purpose (D-023). See router_health_provisioning_test.go.
	ready := func() (bool, string) {
		var noReason string
		return true, noReason
	}
	h := NewRouter(RouterConfig{DBReady: true, ProvisioningReady: ready})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want %d", rec.Code, http.StatusOK)
	}

	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health body: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("GET /health status = %q, want %q", body.Status, "ok")
	}
}

// A control-plane that came up without a database pool mounts none of its
// DB-backed routes: /internal/apikeys/resolve is absent, so edge-api's key
// resolution 404s and every caller is told "Incorrect API key provided". While
// /health answered 200 in that state the compose healthcheck went green and CI
// proceeded, which is how a transient pooler exhaustion at boot (Supabase
// session mode, pool_size 15) turned into a red main with a misleading
// credential error three steps downstream. See issue #816.
func TestNewRouterHealthIsDegradedWithoutDatabase(t *testing.T) {
	h := NewRouter(RouterConfig{DBReady: false})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /health without a database pool = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health body: %v", err)
	}
	if body.Status != "degraded" {
		t.Fatalf("GET /health status = %q, want %q", body.Status, "degraded")
	}
	if body.Reason != "database unavailable" {
		t.Fatalf("GET /health reason = %q, want %q", body.Reason, "database unavailable")
	}
}

// The degraded body is served on a public endpoint, so it must not leak the
// driver's connection error, which carries the database user, host and pooler
// addresses.
func TestNewRouterDegradedHealthDoesNotLeakConnectionDetail(t *testing.T) {
	h := NewRouter(RouterConfig{DBReady: false})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	for _, leak := range []string{"postgres", "pooler", "5432", "password", "supabase"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
			t.Fatalf("degraded /health body leaks %q: %s", leak, rec.Body.String())
		}
	}
}
