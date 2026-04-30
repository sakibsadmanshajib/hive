package invoices

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// =============================================================================
// Phase 14 — Monthly invoice cron.
//
// Runs at 02:00 UTC on day 1 of each month and generates one BDT-only invoice
// row per active workspace covering the prior calendar month. Idempotent:
// re-running the cron for the same period is safe (UNIQUE constraint on
// invoices(workspace_id, period_start) absorbs duplicates).
//
// Per-workspace error isolation: a render or storage failure on one workspace
// must not block invoice generation for any other.
// =============================================================================

// CronConfig configures the monthly cron.
type CronConfig struct {
	// Logger; nil → slog.Default().
	Logger *slog.Logger
	// Interval — how often the loop re-checks the calendar. Default 1 hour.
	// Each pass evaluates whether "now" is the trigger window (day-1 02:00 UTC).
	Interval time.Duration
	// Now is a clock seam for tests; nil → time.Now (UTC).
	Now func() time.Time
}

// Cron orchestrates monthly invoice generation.
type Cron struct {
	svc      *Service
	repo     Repository
	logger   *slog.Logger
	interval time.Duration
	now      func() time.Time

	mu       sync.Mutex
	cancel   context.CancelFunc
	doneCh   chan struct{}
	started  bool
	lastRun  time.Time
	runOnce  sync.Once
}

// NewCron constructs the monthly invoice cron.
func NewCron(svc *Service, repo Repository, cfg CronConfig) *Cron {
	if svc == nil || repo == nil {
		panic("invoices: NewCron requires non-nil service + repo")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Cron{
		svc:      svc,
		repo:     repo,
		logger:   logger,
		interval: interval,
		now:      now,
	}
}

// GenerateMonthlyInvoices runs one pass for the period preceding `now`. It is
// the unit of work the loop schedules and the unit tests exercise. Idempotent.
//
// Returns the count of invoices generated (or already present and unchanged).
func (c *Cron) GenerateMonthlyInvoices(ctx context.Context, now time.Time) (int, error) {
	period := PreviousMonth(now)

	workspaceIDs, err := c.repo.ListActiveWorkspaces(ctx, period)
	if err != nil {
		return 0, fmt.Errorf("invoices: list active workspaces: %w", err)
	}

	generated := 0
	for _, wsID := range workspaceIDs {
		if _, err := c.svc.GenerateInvoiceForPeriod(ctx, wsID, period); err != nil {
			// Per-workspace error isolation — log and continue.
			c.logger.WarnContext(ctx, "invoice cron: workspace generation failed",
				"workspace_id", wsID,
				"period_start", period.Start.Format("2006-01-02"),
				"error", err,
			)
			continue
		}
		generated++
	}
	c.logger.InfoContext(ctx, "invoice cron: pass complete",
		"period_start", period.Start.Format("2006-01-02"),
		"period_end", period.End.Format("2006-01-02"),
		"workspaces_seen", len(workspaceIDs),
		"invoices_generated", generated,
	)
	c.lastRun = now
	return generated, nil
}

// Start launches the monthly loop on a background goroutine. Each tick
// inspects "now" and only generates invoices when the calendar is in the
// day-1 02:00 UTC window AND the previous month has not yet been run.
func (c *Cron) Start(parent context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.doneCh = make(chan struct{})
	c.started = true
	go c.loop(ctx)
}

// Stop signals the loop to exit and waits for the in-flight pass to finish.
func (c *Cron) Stop() {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return
	}
	cancel := c.cancel
	doneCh := c.doneCh
	c.started = false
	c.cancel = nil
	c.doneCh = nil
	c.mu.Unlock()

	cancel()
	<-doneCh
}

func (c *Cron) loop(ctx context.Context) {
	defer close(c.doneCh)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		c.maybeRun(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// maybeRun fires GenerateMonthlyInvoices when the calendar enters the trigger
// window: day-1 of the month, 02:00–02:59 UTC, and the cron has not already
// run this period. Outside that window the call is a no-op.
func (c *Cron) maybeRun(ctx context.Context) {
	now := c.now()
	if now.Day() != 1 || now.Hour() != 2 {
		return
	}
	period := PreviousMonth(now)
	if !c.lastRun.IsZero() && c.lastRun.Year() == now.Year() && c.lastRun.Month() == now.Month() {
		// Already generated for this period; skip.
		return
	}
	if _, err := c.GenerateMonthlyInvoices(ctx, now); err != nil {
		c.logger.WarnContext(ctx, "invoice cron: pass failed",
			"period_start", period.Start.Format("2006-01-02"),
			"error", err,
		)
	}
}
