package proxy_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/proxy"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// hijackableRecorder is an httptest.ResponseRecorder that also implements
// http.Hijacker and http.Flusher so we can verify the statusRecorder wrapper
// delegates correctly.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijackCalled bool
	flushCalled  bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijackCalled = true
	// Return nil conn to keep the test minimal. The wrapper only needs to
	// delegate; callers decide what to do with the result.
	return nil, nil, nil
}

func (h *hijackableRecorder) Flush() {
	h.flushCalled = true
	h.ResponseRecorder.Flush()
}

// plainRecorder does NOT add http.Hijacker. Used to test the
// "non-hijackable ResponseWriter" error path.
type plainRecorder struct {
	*httptest.ResponseRecorder
}

// counterValue extracts the float64 value of a counter matching label key/value pairs.
func counterValue(t *testing.T, cv *dto.MetricFamily, labels map[string]string) float64 {
	t.Helper()
	for _, m := range cv.GetMetric() {
		match := true
		for _, lp := range m.GetLabel() {
			if want, ok := labels[lp.GetName()]; ok && want != lp.GetValue() {
				match = false
				break
			}
		}
		if match {
			return m.GetCounter().GetValue()
		}
	}
	t.Fatalf("no metric matching labels %v in family %s", labels, cv.GetName())
	return 0
}

// gatherFamily returns the named MetricFamily from the registry.
func gatherFamily(t *testing.T, reg interface {
	Gather() ([]*dto.MetricFamily, error)
}, name string) *dto.MetricFamily {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

// ---------------------------------------------------------------------------
// NewEdgeMetrics / MetricsHandler
// ---------------------------------------------------------------------------

func TestNewEdgeMetrics_AllMetricsRegistered(t *testing.T) {
	m, reg := proxy.NewEdgeMetrics()
	if m == nil {
		t.Fatal("NewEdgeMetrics returned nil EdgeMetrics")
	}
	if reg == nil {
		t.Fatal("NewEdgeMetrics returned nil registry")
	}

	// Prometheus CounterVec/HistogramVec only emit metric families in Gather()
	// after at least one label-set has been observed. Prime each family with a
	// zero-value observation so Gather() returns all four expected families.
	m.HTTPRequestsTotal.WithLabelValues("/probe", "GET", "2xx").Add(0)
	m.HTTPRequestDuration.WithLabelValues("/probe").Observe(0)
	m.UpstreamRequestsTotal.WithLabelValues("probe", "ok").Add(0)
	m.UpstreamRequestDuration.WithLabelValues("probe").Observe(0)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := make(map[string]struct{}, len(families))
	for _, f := range families {
		names[f.GetName()] = struct{}{}
	}

	required := []string{
		"hive_http_requests_total",
		"hive_http_request_duration_seconds",
		"hive_upstream_requests_total",
		"hive_upstream_request_duration_seconds",
	}
	for _, want := range required {
		if _, ok := names[want]; !ok {
			t.Errorf("expected metric %q to be registered", want)
		}
	}
}

func TestMetricsHandler_ServesPrometheus(t *testing.T) {
	_, reg := proxy.NewEdgeMetrics()
	h := proxy.MetricsHandler(reg)
	if h == nil {
		t.Fatal("MetricsHandler returned nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content-type, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// InstrumentHandler: metrics increment on request path
// ---------------------------------------------------------------------------

func TestInstrumentHandler_IncrementsRequestCounter(t *testing.T) {
	m, reg := proxy.NewEdgeMetrics()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/chat", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	family := gatherFamily(t, reg, "hive_http_requests_total")
	val := counterValue(t, family, map[string]string{
		"endpoint":     "/v1/chat",
		"method":       "GET",
		"status_class": "2xx",
	})
	if val != 1 {
		t.Errorf("expected counter=1, got %g", val)
	}
}

func TestInstrumentHandler_IncrementsOnErrorStatus(t *testing.T) {
	m, reg := proxy.NewEdgeMetrics()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	family := gatherFamily(t, reg, "hive_http_requests_total")
	val := counterValue(t, family, map[string]string{
		"endpoint":     "/v1/chat/completions",
		"method":       "POST",
		"status_class": "5xx",
	})
	if val != 1 {
		t.Errorf("expected counter=1 for 5xx, got %g", val)
	}
}

func TestInstrumentHandler_RecordsRequestDuration(t *testing.T) {
	m, reg := proxy.NewEdgeMetrics()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	family := gatherFamily(t, reg, "hive_http_request_duration_seconds")
	if len(family.GetMetric()) == 0 {
		t.Fatal("expected at least one duration observation")
	}
	sampleCount := family.GetMetric()[0].GetHistogram().GetSampleCount()
	if sampleCount != 1 {
		t.Errorf("expected 1 duration sample, got %d", sampleCount)
	}
}

func TestInstrumentHandler_NormalizesUUIDsInEndpoint(t *testing.T) {
	m, reg := proxy.NewEdgeMetrics()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/550e8400-e29b-41d4-a716-446655440000/keys", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	family := gatherFamily(t, reg, "hive_http_requests_total")
	val := counterValue(t, family, map[string]string{
		"endpoint": "/v1/accounts/:id/keys",
	})
	if val != 1 {
		t.Errorf("expected UUID to be normalised to :id, got counter %g", val)
	}
}

func TestInstrumentHandler_MultipleRequests_AccumulateCounter(t *testing.T) {
	m, reg := proxy.NewEdgeMetrics()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := proxy.InstrumentHandler(m, inner)

	const n = 5
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}

	family := gatherFamily(t, reg, "hive_http_requests_total")
	val := counterValue(t, family, map[string]string{
		"endpoint": "/v1/models",
		"method":   "GET",
	})
	if val != float64(n) {
		t.Errorf("expected counter=%d, got %g", n, val)
	}
}

// ---------------------------------------------------------------------------
// statusRecorder hijack wrapper: interface delegation
// ---------------------------------------------------------------------------

// TestInstrumentHandler_HijackDelegates_WhenUnderlying verifies that when the
// underlying ResponseWriter implements http.Hijacker, InstrumentHandler's
// wrapper correctly delegates the Hijack() call through.
func TestInstrumentHandler_HijackDelegates_WhenUnderlying(t *testing.T) {
	m, _ := proxy.NewEdgeMetrics()

	hr := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}

	var capturedWriter http.ResponseWriter
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		capturedWriter = w
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	h.ServeHTTP(hr, req)

	hijacker, ok := capturedWriter.(http.Hijacker)
	if !ok {
		t.Fatal("expected wrapped ResponseWriter to implement http.Hijacker")
	}

	conn, _, err := hijacker.Hijack()
	_ = conn
	_ = err
	if !hr.hijackCalled {
		t.Error("expected Hijack() to be delegated to underlying ResponseWriter")
	}
}

// TestInstrumentHandler_HijackError_WhenNotHijackable verifies that calling
// Hijack() on the wrapper returns an error (not a panic) when the underlying
// ResponseWriter does not implement http.Hijacker.
func TestInstrumentHandler_HijackError_WhenNotHijackable(t *testing.T) {
	m, _ := proxy.NewEdgeMetrics()

	pr := &plainRecorder{ResponseRecorder: httptest.NewRecorder()}

	var capturedWriter http.ResponseWriter
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		capturedWriter = w
		w.WriteHeader(http.StatusOK)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodGet, "/plain", nil)
	h.ServeHTTP(pr, req)

	hijacker, ok := capturedWriter.(http.Hijacker)
	if !ok {
		t.Fatal("wrapped ResponseWriter must expose http.Hijacker regardless of underlying support")
	}

	_, _, err := hijacker.Hijack()
	if err == nil {
		t.Error("expected error when underlying ResponseWriter does not implement http.Hijacker")
	}
	if !strings.Contains(err.Error(), "Hijacker") {
		t.Errorf("expected error to mention Hijacker, got: %v", err)
	}
}

// TestInstrumentHandler_FlushDelegates verifies Flush() is forwarded through
// the wrapper without panicking when the underlying ResponseWriter supports it.
func TestInstrumentHandler_FlushDelegates(t *testing.T) {
	m, _ := proxy.NewEdgeMetrics()

	hr := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}

	var capturedWriter http.ResponseWriter
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		capturedWriter = w
		w.WriteHeader(http.StatusOK)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	h.ServeHTTP(hr, req)

	flusher, ok := capturedWriter.(http.Flusher)
	if !ok {
		t.Fatal("expected wrapped ResponseWriter to implement http.Flusher")
	}
	flusher.Flush() // must not panic
	if !hr.flushCalled {
		t.Error("expected Flush() to be delegated to underlying ResponseWriter")
	}
}

// TestInstrumentHandler_NoPanic_NonFlusherUnderlying verifies no panic when
// Flush is called but the underlying writer does not support http.Flusher.
// plainRecorder wraps httptest.ResponseRecorder (which does implement Flusher)
// but the outer wrapper still must not panic regardless.
func TestInstrumentHandler_NoPanic_NonFlusherUnderlying(t *testing.T) {
	m, _ := proxy.NewEdgeMetrics()

	pr := &plainRecorder{ResponseRecorder: httptest.NewRecorder()}

	var capturedWriter http.ResponseWriter
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		capturedWriter = w
		w.WriteHeader(http.StatusOK)
	})
	h := proxy.InstrumentHandler(m, inner)

	req := httptest.NewRequest(http.MethodGet, "/sse-plain", nil)
	h.ServeHTTP(pr, req)

	if flusher, ok := capturedWriter.(http.Flusher); ok {
		flusher.Flush() // must not panic
	}
}
