package http

import (
	"net/http"
	"net/http/httptest"
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
	h := NewRouter(RouterConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want %d", rec.Code, http.StatusOK)
	}
}
