package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/httpx"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/middleware"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/proxy"
)

// deadlineConn records SetReadDeadline calls made through the middleware chain.
type deadlineConn struct {
	http.ResponseWriter
	calls []time.Time
}

func (d *deadlineConn) SetReadDeadline(t time.Time) error {
	d.calls = append(d.calls, t)
	return nil
}

// TestBodyReadDeadlineReachesConnection is the guard that httpx.ReadBody's
// bounded read deadline is not silently inert in production.
//
// http.ResponseController walks the ResponseWriter chain by Unwrap() and stops
// at the first wrapper that neither implements SetReadDeadline nor exposes what
// it wraps. Both wrappers edge-api puts in front of every handler
// (middleware.CompatHeaders' responseRecorder, proxy.InstrumentHandler's
// statusRecorder) are such wrappers, so without their Unwrap methods every
// SetReadDeadline call in this service reports http.ErrNotSupported, ReadBody
// skips the deadline, and its own unit tests still pass because
// httptest.ResponseRecorder does not support deadlines either. This test fails
// if a future wrapper is added to that chain without an Unwrap method.
func TestBodyReadDeadlineReachesConnection(t *testing.T) {
	var handlerRan bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerRan = true
		if _, err := httpx.ReadBody(w, r, 10<<20); err != nil {
			t.Errorf("ReadBody: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	// The exact wrapping order main.go applies around the mux.
	// proxy.NewEdgeMetrics returns (*EdgeMetrics, *prometheus.Registry), not
	// an error: the discarded value is the registry, which only exists to
	// serve /metrics and has no part in this test.
	metrics, _ := proxy.NewEdgeMetrics()
	chain := middleware.CompatHeaders()(proxy.InstrumentHandler(metrics, inner))

	conn := &deadlineConn{ResponseWriter: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	chain.ServeHTTP(conn, req)

	if !handlerRan {
		t.Fatal("inner handler never ran")
	}
	if len(conn.calls) != 2 {
		t.Fatalf("SetReadDeadline reached the connection %d times, want 2 (arm then clear): "+
			"a wrapper in the chain is missing Unwrap() http.ResponseWriter", len(conn.calls))
	}
	if !conn.calls[1].IsZero() {
		t.Fatalf("deadline was not cleared after the read (last call %v); "+
			"a deadline left armed cancels the request context mid-SSE-stream", conn.calls[1])
	}
}
