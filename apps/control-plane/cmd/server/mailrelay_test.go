package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type stubProber struct {
	mu      sync.Mutex
	results []error
	calls   int
	fired   chan struct{}
}

func newStubProber(results ...error) *stubProber {
	return &stubProber{results: results, fired: make(chan struct{}, len(results)+1)}
}

func (s *stubProber) Probe(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := errors.New("stub: no result left")
	if s.calls < len(s.results) {
		err = s.results[s.calls]
	}
	s.calls++
	s.fired <- struct{}{}
	return err
}

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatalf("read gauge: %v", err)
	}
	return m.GetGauge().GetValue()
}

// A failing probe must move the gauge AND stamp the verdict. Only the pair is
// trustworthy: the gauge alone cannot tell "the relay is fine" apart from
// "nothing has run", which is the reading MailRelayVerdictStale exists to
// close.
func TestWatchMailRelay_WritesBothSeriesOnEveryVerdict(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want float64
	}{
		{"healthy relay", nil, 1},
		{"dead relay", errors.New("mailer: dial relay: connection refused"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usable := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_usable"})
			verdict := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_verdict"})
			usable.Set(1)
			verdict.Set(0)

			prober := newStubProber(tc.err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				watchMailRelay(ctx, prober, usable, verdict, time.Hour)
				close(done)
			}()

			select {
			case <-prober.fired:
			case <-time.After(5 * time.Second):
				t.Fatal("the probe never ran")
			}
			// The loop writes both series before it sleeps, so a probe that has
			// fired has already published its verdict.
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("watchMailRelay did not return on context cancellation")
			}

			if got := gaugeValue(t, usable); got != tc.want {
				t.Fatalf("hive_mail_relay_usable = %v, want %v", got, tc.want)
			}
			if got := gaugeValue(t, verdict); got == 0 {
				t.Fatal("the verdict timestamp was never stamped, so the staleness rule would fire on a probe that did run")
			}
		})
	}
}

// Shutdown is not an outage. A probe that fails only because the process is
// going away must not leave 0 behind for the last scrape to read as a broken
// relay.
func TestWatchMailRelay_DoesNotReportShutdownAsAnOutage(t *testing.T) {
	usable := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_usable_shutdown"})
	verdict := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_verdict_shutdown"})
	usable.Set(1)
	verdict.Set(0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		watchMailRelay(ctx, newStubProber(context.Canceled), usable, verdict, time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchMailRelay did not return for an already-cancelled context")
	}

	if got := gaugeValue(t, usable); got != 1 {
		t.Fatalf("hive_mail_relay_usable = %v after shutdown, want the last real verdict (1)", got)
	}
	if got := gaugeValue(t, verdict); got != 0 {
		t.Fatalf("the verdict timestamp was stamped during shutdown (%v); a cancelled probe is not a verdict", got)
	}
}

// No relay configured means no series at all. A gauge sitting at 0 on a stack
// that sends no mail is a permanent false page, and an alert that always fires
// is one nobody reads.
func TestStartMailRelayWatch_ExportsNothingWithoutARelay(t *testing.T) {
	t.Setenv("HIVE_SMTP_HOST", "")
	t.Setenv("HIVE_MAIL_FROM", "")

	reg := prometheus.NewRegistry()
	if startMailRelayWatch(context.Background(), reg) {
		t.Fatal("startMailRelayWatch started a probe with no relay configured")
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "hive_mail_relay_usable" || f.GetName() == "hive_mail_relay_last_verdict_seconds" {
			t.Fatalf("%s was exported on a deployment with no relay", f.GetName())
		}
	}
}
