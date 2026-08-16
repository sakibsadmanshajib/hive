package inference

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// stageSampleCount returns how many observations the histogram holds for one
// (stage, endpoint) pair, and whether that series exists at all.
func stageSampleCount(t *testing.T, reg *prometheus.Registry, stage, endpoint string) (uint64, bool) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "hive_metered_stage_duration_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			var gotStage, gotEndpoint string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "stage":
					gotStage = l.GetValue()
				case "endpoint":
					gotEndpoint = l.GetValue()
				}
			}
			if gotStage == stage && gotEndpoint == endpoint {
				return m.GetHistogram().GetSampleCount(), true
			}
		}
	}
	return 0, false
}

func TestStageRecordsOneObservationPerCall(t *testing.T) {
	reg := prometheus.NewRegistry()
	o := &Orchestrator{}
	o.WithStageMetrics(NewStageMetrics(reg))

	done := o.stage("chat_completions", StageCreateReservation)
	time.Sleep(time.Millisecond)
	done()

	count, ok := stageSampleCount(t, reg, StageCreateReservation, "chat_completions")
	if !ok {
		t.Fatal("expected a hive_metered_stage_duration_seconds series for create_reservation")
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 observation, got %d", count)
	}

	// A second stage on the same orchestrator must land on its own series, so
	// the split between provider time and Hive's own time stays readable.
	o.stage("chat_completions", StageDispatch)()
	if _, ok := stageSampleCount(t, reg, StageDispatch, "chat_completions"); !ok {
		t.Fatal("expected a separate series for the dispatch stage")
	}
}

// The observation must survive being recorded even when the duration is long:
// the whole reason this metric exists is that stages routinely run past
// prometheus.DefBuckets' 10 s ceiling, and a bucket set that tops out below the
// interesting values reports nothing about them.
func TestStageBucketsCoverTheObservedRange(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewStageMetrics(reg)
	m.observe(StageTotal, "chat_completions", 24*time.Second)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var hist *dto.Histogram
	for _, f := range families {
		if f.GetName() == "hive_metered_stage_duration_seconds" {
			hist = f.GetMetric()[0].GetHistogram()
		}
	}
	if hist == nil {
		t.Fatal("expected the histogram to be registered")
	}
	var highestFiniteBound float64
	for _, b := range hist.GetBucket() {
		if b.GetUpperBound() > highestFiniteBound {
			highestFiniteBound = b.GetUpperBound()
		}
	}
	if highestFiniteBound < 24 {
		t.Fatalf("a 24 s observation must land in a finite bucket, but the highest bound is %.2fs", highestFiniteBound)
	}
}

// A total that is only recorded after a successful response write is a metric
// that cannot go red on the outcomes worth alerting on. executeSync returns
// early on authorization, routing, reservation, upstream and normalization
// failures, so the total has to be deferred. This drives the earliest of those
// exits (authorization) and asserts the observation still lands.
func TestTotalIsRecordedOnAFailedRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	orch := upstreamUnavailableOrchestrator().WithStageMetrics(NewStageMetrics(reg))

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", NeedFlags{}, 100, nil, nil)

	if w.Code == 200 {
		t.Fatalf("this test needs a failing request to be meaningful, got 200")
	}
	count, ok := stageSampleCount(t, reg, StageTotal, EndpointChatCompletions)
	if !ok || count != 1 {
		t.Fatalf("a failed request must still record one total observation, got count=%d present=%v", count, ok)
	}
	// The stage that did run before the failure is recorded too, so the total
	// can be read against its parts rather than standing alone.
	if _, ok := stageSampleCount(t, reg, StageAuthorize, EndpointChatCompletions); !ok {
		t.Fatal("expected the authorize stage to be recorded on the failure path")
	}
}

// Instrumentation must never be the reason a request fails. Every existing
// construction site builds an Orchestrator without metrics.
func TestStageIsNilSafeWithoutMetrics(t *testing.T) {
	o := &Orchestrator{}
	o.stage("chat_completions", StageTotal)() // must not panic

	var m *StageMetrics
	m.observe(StageTotal, "chat_completions", time.Second) // must not panic
}
