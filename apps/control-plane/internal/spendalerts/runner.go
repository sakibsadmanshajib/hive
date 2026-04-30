// Package spendalerts orchestrates threshold-based workspace spend alerting.
//
// Phase 14 separates the *evaluation* logic (which lives in the budgets package
// as `budgets.CronEvaluator`) from the *runner* loop (here): a thin scheduler
// that ticks the evaluator on a configurable interval and exposes hooks for
// process-lifecycle integration (Start, Stop, RunOnce).
//
// Threshold math (50% / 80% / 100%) is implemented in `budgets.ThresholdCrossed`.
// One-shot per crossed threshold per period is enforced by stamping
// `spend_alerts.last_fired_period`, owned by the evaluator. The runner contains
// no business logic — it is a stable surface for control-plane wiring + tests.
package spendalerts

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Evaluator is the small surface the runner depends on. It matches
// `budgets.CronEvaluator.EvaluateBudgets` so the budgets package's evaluator
// drops in directly. Defined here per Go's interface-where-used convention.
type Evaluator interface {
	EvaluateBudgets(ctx context.Context, now time.Time) (int, error)
}

// Config controls Runner behaviour.
type Config struct {
	// Interval between evaluation passes. Zero defaults to 60s.
	Interval time.Duration
	// Logger; nil → slog.Default().
	Logger *slog.Logger
	// Now is a clock seam for tests; nil → time.Now (UTC).
	Now func() time.Time
}

// Runner ticks an Evaluator on a fixed cadence until Stop is called.
//
// The runner is idempotent and safe to start once per process. Per-pass errors
// are logged but do NOT halt the loop — alert dispatch must survive transient
// repository or notifier failures.
type Runner struct {
	eval     Evaluator
	interval time.Duration
	logger   *slog.Logger
	now      func() time.Time

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	started bool
}

// NewRunner builds a Runner. `eval` must be non-nil; `cfg` zero-values are
// replaced with defaults.
func NewRunner(eval Evaluator, cfg Config) *Runner {
	if eval == nil {
		panic("spendalerts: nil evaluator")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{
		eval:     eval,
		interval: interval,
		logger:   logger,
		now:      now,
	}
}

// RunOnce performs exactly one evaluation pass synchronously. It is the unit
// of work the loop schedules and the unit tests exercise.
//
// Returns the number of alerts fired during the pass, plus any error returned
// by the evaluator. Per-workspace errors are absorbed inside the evaluator.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	now := r.now()
	fired, err := r.eval.EvaluateBudgets(ctx, now)
	if err != nil {
		r.logger.WarnContext(ctx, "spendalerts: evaluation pass failed",
			"error", err, "now", now.Format(time.RFC3339))
		return fired, fmt.Errorf("spendalerts: evaluate: %w", err)
	}
	if fired > 0 {
		r.logger.InfoContext(ctx, "spendalerts: alerts fired",
			"count", fired, "now", now.Format(time.RFC3339))
	}
	return fired, nil
}

// Start launches the runner loop on a background goroutine. Subsequent Start
// calls are no-ops until Stop is called. The supplied parent context controls
// loop cancellation in addition to Stop.
func (r *Runner) Start(parent context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.doneCh = make(chan struct{})
	r.started = true

	go r.loop(ctx)
}

// Stop signals the loop to exit and waits for the in-flight pass to finish.
// Safe to call multiple times.
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	cancel := r.cancel
	doneCh := r.doneCh
	r.started = false
	r.cancel = nil
	r.doneCh = nil
	r.mu.Unlock()

	cancel()
	<-doneCh
}

func (r *Runner) loop(ctx context.Context) {
	defer close(r.doneCh)

	// Eager first pass so a service start surfaces breaches without a full
	// interval delay.
	if _, err := r.RunOnce(ctx); err != nil {
		// already logged
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.RunOnce(ctx); err != nil {
				// already logged
			}
		}
	}
}
