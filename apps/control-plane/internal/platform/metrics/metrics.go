package metrics

import (
	"errors"

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

	// SignupProvisioningSweepFailures is how many provisioning sweeps have failed
	// in a row. Provisioning can be broken while every other route still works,
	// so this is reported here rather than on the readiness endpoint the container
	// healthcheck reads: an alert can fire without taking the process out of
	// service and leaving inference and billing without a control-plane.
	SignupProvisioningSweepFailures prometheus.Gauge

	// BudgetMTDCounterWriteFailures counts settlements whose spend could not be
	// recorded against the workspace month-to-date counter the edge-api budget
	// gate reads. Each one is a charge missing from a customer's hard cap until
	// the spend-alert pass restates it from the ledger.
	//
	// The edge side has its own counter for the reader failing open. This is the
	// writer half, and without it a completely dead writer and a healthy one both
	// report zero everywhere, which is the ambiguity issue #1651 was made of.
	BudgetMTDCounterWriteFailures prometheus.Counter

	// BudgetMTDCounterWired is 1 when this process can write that counter and 0
	// when it cannot, which happens when Redis was unreachable at boot or the
	// conversion rate was unusable. At 0 no workspace hard cap is enforced for
	// the life of the process, and no failure counter anywhere will move, because
	// nothing is attempted. Alert on this, not only on the failure counters.
	BudgetMTDCounterWired prometheus.Gauge
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
		SignupProvisioningSweepFailures: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "hive_signup_provisioning_sweep_failures",
			Help: "Consecutive failed signup provisioning sweeps, 0 when the last sweep completed",
		}),
		BudgetMTDCounterWriteFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "hive_budget_mtd_counter_write_failures_total",
			Help: "Settled charges that could not be recorded against the workspace month-to-date spend counter",
		}),
		BudgetMTDCounterWired: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "hive_budget_mtd_counter_wired",
			Help: "1 when this control-plane can write the budget month-to-date counter, 0 when workspace hard caps are unenforced",
		}),
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
		r.SignupProvisioningSweepFailures,
		r.BudgetMTDCounterWriteFailures,
		r.BudgetMTDCounterWired,
	)
	return r, reg
}

// SignupProvisioningSource is the reconciler state RegisterSignupProvisioning
// exports. Declared as an interface here so the metrics package does not import
// the signup package, and satisfied by *signup.Reconciler.
//
// Both accessors must be cheap and non-blocking: they are read during a
// Prometheus scrape, so neither may touch the database. The reconciler caches
// the stranded count on its own sweep timer for exactly this reason.
type SignupProvisioningSource interface {
	Faults() int
	StrandedIdentities() int
}

// RegisterSignupProvisioning adds the two metrics that make a permanently
// failed provisioning visible. They are function-backed collectors rather than
// fields on Registry because their values are owned by the reconciler, which
// already holds them under its own lock; copying them across on a ticker would
// add a second source of truth that can lag or stall.
//
// Why two metrics rather than one. hive_signup_provisioning_sweep_failures
// answers "is provisioning failing right now" and resets on any clean sweep,
// which is correct for that question and useless for the one that matters: a
// sweep goes clean the moment the identity that kept faulting ages out of the
// 24 hour lookback window, so an alert on that gauge alone resolves itself at
// the exact instant the loss becomes permanent. A monotonic counter cannot be
// walked back that way, and a gauge of identities already past the window
// reports the standing consequence rather than the event. Neither substitutes
// for the other: the counter says work was lost, the gauge says who is still
// affected.
func RegisterSignupProvisioning(reg *prometheus.Registry, src SignupProvisioningSource) error {
	if reg == nil || src == nil {
		return errors.New("metrics: signup provisioning collectors need a registry and a source")
	}
	collectors := []prometheus.Collector{
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "hive_signup_provisioning_faults_total",
			Help: "Identities that could not be provisioned, monotonic since process start",
		}, func() float64 { return float64(src.Faults()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "hive_signup_provisioning_stranded_identities",
			Help: "Identities holding no tenant membership that are already older than the provisioning sweep's lookback window, so no sweep will retry them",
		}, func() float64 { return float64(src.StrandedIdentities()) }),
	}
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}
