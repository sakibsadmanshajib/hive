package agenttask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// StatusChecker is the narrow surface the Poller needs from an Engine
// implementation. apps/control-plane/internal/agentengine.Engine already has
// a method with this exact signature (agenttask.Status is its own return
// type), so it satisfies this interface with no adapter code — Go
// interfaces are structural.
type StatusChecker interface {
	Status(ctx context.Context, sessionRef string) (status Status, resultSummary, errMessage string, err error)
}

// maxPollerBackoff caps the exponential backoff RunOnce failures grow the
// loop's interval to (see loop's doc comment).
const maxPollerBackoff = 5 * time.Minute

// PollerConfig controls Poller behaviour.
type PollerConfig struct {
	// Interval between poll passes when the previous pass had no errors.
	// Zero defaults to 15s.
	Interval time.Duration
	// Logger; nil defaults to slog.Default().
	Logger *slog.Logger
}

// Poller periodically advances every active (queued/running, launched) task
// to its terminal state: lists them (Repository.ListActive), polls each
// one's engine status (StatusChecker.Status), and atomically transitions
// terminal ones (Repository.Transition). Mirrors
// apps/control-plane/internal/spendalerts.Runner's Start/Stop/RunOnce shape.
type Poller struct {
	repo     Repository
	checker  StatusChecker
	interval time.Duration
	logger   *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	started bool
}

// NewPoller builds a Poller. repo and checker must be non-nil.
func NewPoller(repo Repository, checker StatusChecker, cfg PollerConfig) *Poller {
	if repo == nil {
		panic("agenttask: nil repository")
	}
	if checker == nil {
		panic("agenttask: nil status checker")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{repo: repo, checker: checker, interval: interval, logger: logger}
}

// RunOnce performs exactly one poll pass: every active task gets exactly one
// StatusChecker.Status call and, if terminal, one Repository.Transition
// call. A single task's engine error is logged and skipped — it stays
// active and is retried next pass, it does not abort the rest of the pass.
// The returned error, when non-nil, reports that at least one task had a
// problem this pass (see loop's backoff); it does not mean the pass failed
// outright unless ListActive itself errored.
func (p *Poller) RunOnce(ctx context.Context) (advanced int, err error) {
	tasks, err := p.repo.ListActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("agenttask: poller list active: %w", err)
	}

	var errCount int
	for _, t := range tasks {
		status, resultSummary, errMessage, statusErr := p.checker.Status(ctx, t.EngineSessionRef)
		if statusErr != nil {
			errCount++
			p.logger.WarnContext(ctx, "agenttask: poller status check failed, retrying next pass",
				"task_id", t.ID, "error", statusErr)
			continue
		}
		if status != StatusSucceeded && status != StatusFailed && status != StatusCancelled {
			continue
		}

		// Provider-blind boundary: errMessage came from StatusChecker, an
		// interface this package does not control the implementation of.
		// error_message is customer-visible (Handler.handleGet), so a raw
		// engine/provider detail must never reach it — log the real detail
		// server-side, persist a generic message instead.
		if errMessage != "" {
			p.logger.WarnContext(ctx, "agenttask: task failed, engine detail",
				"task_id", t.ID, "engine_detail", errMessage)
			errMessage = "agent task failed"
		}

		if _, transErr := p.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, status, "", resultSummary, errMessage); transErr != nil {
			if errors.Is(transErr, ErrTerminalState) {
				// Lost a race with a concurrent Cancel (or a previous pass
				// that already advanced this task): already terminal,
				// nothing left to do.
				continue
			}
			errCount++
			p.logger.WarnContext(ctx, "agenttask: poller transition failed, retrying next pass",
				"task_id", t.ID, "error", transErr)
			continue
		}
		advanced++
	}

	if errCount > 0 {
		return advanced, fmt.Errorf("agenttask: poller pass had %d task-level error(s)", errCount)
	}
	return advanced, nil
}

// Start launches the poll loop on a background goroutine. Subsequent Start
// calls are no-ops until Stop is called.
func (p *Poller) Start(parent context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	doneCh := make(chan struct{})
	p.cancel = cancel
	p.doneCh = doneCh
	p.started = true

	go p.loop(ctx, doneCh)
}

// Stop signals the loop to exit and waits for the in-flight pass to finish.
// Safe to call multiple times. started/cancel/doneCh are cleared only AFTER
// the wait: clearing them first would let a concurrent Start launch a
// second loop while the previous pass is still running (duplicate status
// checks, competing terminal transitions). The p.doneCh == doneCh check
// guards a concurrent second Stop from clearing state a subsequent
// Start/Stop cycle has already replaced.
func (p *Poller) Stop() {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return
	}
	cancel := p.cancel
	doneCh := p.doneCh
	p.mu.Unlock()

	cancel()
	<-doneCh

	p.mu.Lock()
	if p.doneCh == doneCh {
		p.started = false
		p.cancel = nil
		p.doneCh = nil
	}
	p.mu.Unlock()
}

// loop ticks RunOnce on p.interval, doubling the delay (capped at
// maxPollerBackoff) each pass that reports an error and resetting to
// p.interval on the first clean pass — an engine outage or a run of DB
// errors backs the poller off instead of hammering at full frequency, while
// a single transient task-level error only costs one doubled wait, not a
// standing degraded state.
func (p *Poller) loop(ctx context.Context, doneCh chan<- struct{}) {
	defer close(doneCh)

	consecutiveFailures := 0
	runPass := func() {
		if _, err := p.RunOnce(ctx); err != nil {
			consecutiveFailures++
		} else {
			consecutiveFailures = 0
		}
	}

	// Eager first pass so a service start advances already-active tasks
	// without waiting a full interval.
	runPass()

	timer := time.NewTimer(pollerBackoffDelay(p.interval, consecutiveFailures))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			runPass()
			timer.Reset(pollerBackoffDelay(p.interval, consecutiveFailures))
		}
	}
}

// pollerBackoffDelay returns the delay before the next pass:
// consecutiveFailures 0 → base; each further failure doubles it, capped at
// maxPollerBackoff. Pure function, kept separate from loop for testing
// without timers.
func pollerBackoffDelay(base time.Duration, consecutiveFailures int) time.Duration {
	d := base
	for i := 0; i < consecutiveFailures && d < maxPollerBackoff; i++ {
		d *= 2
	}
	if d > maxPollerBackoff {
		d = maxPollerBackoff
	}
	return d
}
