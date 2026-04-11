// Package proxy provides shared proxy-level Prometheus metrics for the edge-api.
// It exposes a lightweight set of HTTP and upstream metrics that are registered
// on a custom registry (not prometheus.DefaultRegistry).
package proxy

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// EdgeMetrics holds all edge-api Prometheus metrics.
type EdgeMetrics struct {
	HTTPRequestsTotal       *prometheus.CounterVec
	HTTPRequestDuration     *prometheus.HistogramVec
	UpstreamRequestsTotal   *prometheus.CounterVec
	UpstreamRequestDuration *prometheus.HistogramVec
}

// NewEdgeMetrics creates and registers edge-api metrics on a fresh custom registry.
// Returns the EdgeMetrics for instrumentation and the prometheus.Registry for /metrics.
func NewEdgeMetrics() (*EdgeMetrics, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	m := &EdgeMetrics{
		HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hive_http_requests_total",
			Help: "Total HTTP requests by endpoint, method, and status class",
		}, []string{"endpoint", "method", "status_class"}),
		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hive_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds by endpoint",
			Buckets: prometheus.DefBuckets,
		}, []string{"endpoint"}),
		UpstreamRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hive_upstream_requests_total",
			Help: "Total upstream provider requests by provider and status",
		}, []string{"provider", "status"}),
		UpstreamRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hive_upstream_request_duration_seconds",
			Help:    "Upstream provider request duration in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"provider"}),
	}
	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.UpstreamRequestsTotal,
		m.UpstreamRequestDuration,
	)
	return m, reg
}

// MetricsHandler returns an http.Handler that serves Prometheus metrics
// from the given prometheus.Registry.
func MetricsHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// statusRecorder wraps http.ResponseWriter to capture the HTTP status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// uuidPattern matches UUID segments in URL paths.
var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// normalizeEndpoint replaces UUID segments with ":id" to prevent cardinality explosion.
func normalizeEndpoint(path string) string {
	return uuidPattern.ReplaceAllString(path, ":id")
}

// InstrumentHandler wraps an http.Handler with edge-api request metrics.
func InstrumentHandler(m *EdgeMetrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		statusClass := fmt.Sprintf("%dxx", recorder.status/100)
		endpoint := normalizeEndpoint(r.URL.Path)
		m.HTTPRequestsTotal.WithLabelValues(endpoint, r.Method, statusClass).Inc()
		m.HTTPRequestDuration.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
	})
}
