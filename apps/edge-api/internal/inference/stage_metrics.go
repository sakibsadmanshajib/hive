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

// cacheBillingMagnitudeGuardTrips and cacheBillingFallbackRateUsed are
// package-level rather than fields on StageMetrics: assertCacheBillingMagnitude
// and resolveCacheRate (pricing.go) are plain functions reached from both the
// API-key orchestrator (which carries a *StageMetrics) and the session-chat
// package (apps/edge-api/internal/chat, which does not), so a field would
// need threading through every call site on the settlement path in two
// packages for a metrics-only feature. A package-level *prometheus.CounterVec
// is safe here: it is one Go object regardless of who increments it,
// registration happens once at startup (NewStageMetrics, below) on the same
// registry apps/edge-api/internal/proxy.NewEdgeMetrics already serves at
// /metrics, and a test that never calls NewStageMetrics simply never
// registers them -- WithLabelValues(...).Inc() on an unregistered CounterVec
// is a normal, harmless no-op observation, the same nil-safety principle
// StageMetrics.observe already relies on for the duration histogram.
var (
	cacheBillingMagnitudeGuardTrips = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_cache_billing_magnitude_guard_trips_total",
		Help: "Times the cache-aware billing magnitude guard fired: a charge exceeded 2x the highest-rate bound (the highest of the input, cache-read and cache-write rates), the signature of a cache semantics inversion (vault spec-2026-08-25-cache-aware-billing.md).",
	}, []string{"alias", "provider"})
	cacheBillingFallbackRateUsed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_cache_billing_fallback_rate_used_total",
		Help: "Times a cache charge fell back to the documented default multiplier because the alias's catalog cache price was unset (NULL, not a deliberate zero), by alias, provider and side (read/write).",
	}, []string{"alias", "provider", "side"})
	streamUsageBlockMissing = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_stream_usage_block_missing_total",
		Help: "Streams that delivered output while the upstream sent no usable usage block; settled at the reservation hold instead of an estimate or zero (#1215, D-034). A rising rate means a provider stopped honouring stream_options.include_usage or shipped unparseable usage frames.",
	}, []string{"alias", "endpoint"})
	zeroContentCaptureTrips = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_zero_content_captured_total",
		Help: "Sync chat completions that returned no visible content even after one retry and settled fail-closed by capturing the reservation hold instead of full price (issue #1171), by alias and endpoint.",
	}, []string{"alias", "endpoint"})
	streamRelayAborted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_stream_relay_aborted_total",
		Help: "SSE relay loops (chat completions and Responses API) that ended on a genuine scanner read/token error rather than [DONE] or a client disconnect -- most commonly bufio.ErrTooLong, a single upstream line over the scanner's buffer limit (issue #1255). A client disconnect never increments this: ctx.Err() != nil is excluded so routine cancellations, the overwhelming majority of stream endings, do not bury the signal this counter exists to surface.",
	}, []string{"alias", "endpoint"})
)

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
	// zeroContentCaptureTrips is deliberately not in this list: it is a
	// pre-existing state (declared but never registered), out of this PR's
	// scope to change. streamRelayAborted is added here, alongside the
	// counters that already were.
	reg.MustRegister(m.duration, cacheBillingMagnitudeGuardTrips, cacheBillingFallbackRateUsed, streamUsageBlockMissing, streamRelayAborted)
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
// Deliberately not defer-only: most of these stages sit in the middle of a
// long function whose scope is the whole request, so deferring them all would
// record every stage at the same moment, at the end. A stage that genuinely
// does span the whole function (StageTotal) should still be deferred, so that
// it is recorded on the early-return failure paths too.
func (o *Orchestrator) stage(endpoint, name string) func() {
	if o.metrics == nil {
		return func() {}
	}
	start := time.Now()
	return func() { o.metrics.observe(name, endpoint, time.Since(start)) }
}
