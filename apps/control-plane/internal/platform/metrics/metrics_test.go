package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hivegpt/hive/apps/control-plane/internal/platform/metrics"
)

func TestNewRegistry(t *testing.T) {
	reg, promReg := metrics.NewRegistry()
	if reg == nil || promReg == nil {
		t.Fatal("NewRegistry returned nil")
	}

	// Increment several counters so the families show up in Gather output.
	reg.HTTPRequestsTotal.WithLabelValues("/test", "GET", "2xx").Inc()
	reg.PaymentEventsTotal.WithLabelValues("stripe", "success").Inc()
	reg.LedgerPostingsTotal.WithLabelValues("grant").Inc()
	reg.RateLimitHitsTotal.WithLabelValues("free").Inc()
	reg.AuthFailuresTotal.WithLabelValues("invalid_token").Inc()

	families, err := promReg.Gather()
	if err != nil {
		t.Fatalf("Gather after increment failed: %v", err)
	}
	if len(families) < 5 {
		t.Errorf("expected at least 5 metric families with data, got %d", len(families))
	}
}

func TestInstrumentHandler(t *testing.T) {
	reg, _ := metrics.NewRegistry()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	handler := metrics.InstrumentHandler(reg, inner)
	req := httptest.NewRequest("GET", "/api/v1/accounts/current/credits/balance", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMetricsEndpointServesValidPrometheus(t *testing.T) {
	reg, promReg := metrics.NewRegistry()
	reg.HTTPRequestsTotal.WithLabelValues("/health", "GET", "2xx").Inc()
	handler := promhttp.HandlerFor(promReg, promhttp.HandlerOpts{})
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics endpoint returned %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hive_http_requests_total") {
		t.Error("metrics output missing hive_http_requests_total")
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	// Verify UUID paths are normalized by checking label values via InstrumentHandler.
	reg, promReg := metrics.NewRegistry()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := metrics.InstrumentHandler(reg, inner)

	// Request with UUID in path.
	req := httptest.NewRequest("GET", "/api/v1/accounts/current/api-keys/550e8400-e29b-41d4-a716-446655440000/policy", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Gather metrics and verify the endpoint label does not contain the raw UUID.
	families, _ := promReg.Gather()
	for _, f := range families {
		if f.GetName() == "hive_http_requests_total" {
			for _, m := range f.GetMetric() {
				for _, lp := range m.GetLabel() {
					if lp.GetName() == "endpoint" && strings.Contains(lp.GetValue(), "550e8400") {
						t.Error("endpoint label contains raw UUID — cardinality explosion risk")
					}
				}
			}
		}
	}
}
