package inference

import (
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

// Instrumentation must never be the reason a request fails. Every existing
// construction site builds an Orchestrator without metrics.
func TestStageIsNilSafeWithoutMetrics(t *testing.T) {
	o := &Orchestrator{}
	o.stage("chat_completions", StageTotal)() // must not panic

	var m *StageMetrics
	m.observe(StageTotal, "chat_completions", time.Second) // must not panic
}
