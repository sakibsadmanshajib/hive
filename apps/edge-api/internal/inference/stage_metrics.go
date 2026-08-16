package inference

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// StageMetrics records how long each stage of a metered inference request
// takes, so the split between provider time and Hive's own time is a standing
// signal rather than something that has to be re-derived by hand.
//
// It exists because that re-derivation was expensive and the conclusion was
// counter-intuitive. Measured on the demo box on 2026-08-16, one metered
// chat completion took 12.4 to 24.4 seconds end to end while the same prompt
// straight through LiteLLM took 0.54 to 1.38 seconds. The gap was not the
// provider, not the network (the full public round trip is 0.13 s), not DNS,
// and not database execution (one request spends 216 ms of database server
// time). It was the number of sequential statements the request makes against
// a database roughly 240 ms away: about 86 of them, which multiplies out to
// very nearly the observed wall clock.
//
// Nothing above is visible from the outside, which is the point of this type.
// A per-stage histogram makes the same finding readable from /metrics in one
// query, and makes a regression in any single stage obvious immediately.
type StageMetrics struct {
	duration *prometheus.HistogramVec
}

// Stage names. Fixed set, so the label stays low cardinality and a dashboard
// can name the series it wants.
const (
	StageAuthorize           = "authorize"
	StageSelectRoute         = "select_route"
	StageStartAttempt        = "start_attempt"
	StageCreateReservation   = "create_reservation"
	StageDispatch            = "dispatch"
	StageFinalizeReservation = "finalize_reservation"
	StageRecordUsage         = "record_usage"
	StageResponseWrite       = "response_write"
	StageTotal               = "total"
)

// NewStageMetrics registers the stage histogram on reg.
//
// Buckets deliberately reach far past prometheus.DefBuckets, whose top bucket
// is 10 s: the stages this measures are routinely 5 to 7 s each today and the
// total has been observed at 24 s, so DefBuckets would put almost every
// observation in +Inf and report nothing useful about exactly the values that
// matter. 0.05 s doubling twelve times covers 50 ms to 102 s.
func NewStageMetrics(reg prometheus.Registerer) *StageMetrics {
	m := &StageMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hive_metered_stage_duration_seconds",
			Help:    "Duration of each stage of a metered inference request, by stage and endpoint.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 12),
		}, []string{"stage", "endpoint"}),
	}
	reg.MustRegister(m.duration)
	return m
}

// observe records one stage duration. Nil-safe on purpose: an Orchestrator
// built without metrics (every existing test does exactly that) records
// nothing rather than panicking, so instrumentation can never be the reason a
// request fails.
func (m *StageMetrics) observe(stage, endpoint string, d time.Duration) {
	if m == nil || m.duration == nil {
		return
	}
	m.duration.WithLabelValues(stage, endpoint).Observe(d.Seconds())
}

// stage starts timing one stage and returns the function that ends it. The
// returned function is safe to call exactly once; calling it is what records
// the observation.
//
//	done := o.stage(endpoint, StageCreateReservation)
//	reservation, err := o.accounting.CreateReservation(ctx, ...)
//	done()
//
// Deliberately not a defer-based helper: several of these stages sit in the
// middle of a long function whose scope is the whole request, so a defer would
// record every stage at the same moment, at the end.
func (o *Orchestrator) stage(endpoint, name string) func() {
	if o.metrics == nil {
		return func() {}
	}
	start := time.Now()
	return func() { o.metrics.observe(name, endpoint, time.Since(start)) }
}
