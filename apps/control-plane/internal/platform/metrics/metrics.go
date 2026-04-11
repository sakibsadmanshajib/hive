package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Registry holds all application-level Prometheus metrics.
// Uses a custom registry (not prometheus.DefaultRegistry) to exclude
// Go runtime metrics from the application /metrics endpoint.
type Registry struct {
	HTTPRequestsTotal       *prometheus.CounterVec
	HTTPRequestDuration     *prometheus.HistogramVec
	UpstreamRequestsTotal   *prometheus.CounterVec
	UpstreamRequestDuration *prometheus.HistogramVec
	PaymentEventsTotal      *prometheus.CounterVec
	LedgerPostingsTotal     *prometheus.CounterVec
	RateLimitHitsTotal      *prometheus.CounterVec
	AuthFailuresTotal       *prometheus.CounterVec
}

// NewRegistry creates and registers all Prometheus metrics.
// Returns the application Registry and the underlying prometheus.Registry
// (used to serve /metrics via promhttp.HandlerFor).
func NewRegistry() (*Registry, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	r := &Registry{
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
		PaymentEventsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hive_payment_events_total",
			Help: "Total payment events by rail and status",
		}, []string{"rail", "status"}),
		LedgerPostingsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hive_ledger_postings_total",
			Help: "Total ledger postings by entry type",
		}, []string{"entry_type"}),
		RateLimitHitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hive_rate_limit_hits_total",
			Help: "Total rate limit hits by tier",
		}, []string{"tier"}),
		AuthFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hive_auth_failures_total",
			Help: "Total auth failures by reason",
		}, []string{"reason"}),
	}
	reg.MustRegister(
		r.HTTPRequestsTotal,
		r.HTTPRequestDuration,
		r.UpstreamRequestsTotal,
		r.UpstreamRequestDuration,
		r.PaymentEventsTotal,
		r.LedgerPostingsTotal,
		r.RateLimitHitsTotal,
		r.AuthFailuresTotal,
	)
	return r, reg
}
