package budgets

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Phase 14 — Spend alert cron evaluator.
//
// Walks workspaces with active budgets, computes month-to-date spend in BDT
// subunits (math/big), compares against each configured alert threshold, and
// fires the notifier exactly once per (alert, period). Idempotency is enforced
// by stamping last_fired_period in the spend_alerts row.
//
// Threshold cross math (no float division):
//
//	mtd * 100 >= soft_cap * threshold_pct
//
// All operands are *big.Int; the inequality uses Cmp on a temporary big.Int.
// =============================================================================

// CronEvaluator owns the spend-alert evaluation pass.
type CronEvaluator struct {
	repo     WorkspaceBudgetRepository
	notifier AlertNotifier
	logger   *slog.Logger
	now      func() time.Time
}

// NewCronEvaluator constructs a CronEvaluator.
func NewCronEvaluator(repo WorkspaceBudgetRepository, notifier AlertNotifier, logger *slog.Logger) *CronEvaluator {
	if logger == nil {
		logger = slog.Default()
	}
	return &CronEvaluator{
		repo:     repo,
		notifier: notifier,
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// EvaluateBudgets runs one evaluation pass. It is safe to call repeatedly: each
// (alert, period) fires once thanks to last_fired_period stamping.
//
// Returns the number of alerts fired in this pass (for tests + metrics).
func (c *CronEvaluator) EvaluateBudgets(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = c.now()
	}
	period := startOfMonthUTC(now)

	workspaceIDs, err := c.repo.ListWorkspacesWithBudget(ctx)
	if err != nil {
		return 0, fmt.Errorf("budgets: list workspaces with budget: %w", err)
	}

	fired := 0
	for _, wsID := range workspaceIDs {
		n, err := c.evaluateWorkspace(ctx, wsID, now, period)
		if err != nil {
			// Per-workspace error isolation — log and continue.
			c.logger.WarnContext(ctx, "budget cron: workspace evaluation failed",
				"workspace_id", wsID, "error", err)
			continue
		}
		fired += n
	}
	return fired, nil
}

func (c *CronEvaluator) evaluateWorkspace(ctx context.Context, wsID uuid.UUID, now, period time.Time) (int, error) {
	budget, err := c.repo.GetBudget(ctx, wsID)
	if err != nil {
		return 0, fmt.Errorf("get budget: %w", err)
	}
	if budget == nil {
		return 0, nil
	}
	if budget.SoftCap == nil || budget.SoftCap.Sign() == 0 {
		// Soft cap of 0 disables percentage-based alerts (would div by zero).
		return 0, nil
	}

	alerts, err := c.repo.ListAlerts(ctx, wsID)
	if err != nil {
		return 0, fmt.Errorf("list alerts: %w", err)
	}
	if len(alerts) == 0 {
		return 0, nil
	}

	mtd, err := c.repo.MonthToDateSpendBDT(ctx, wsID, period)
	if err != nil {
		return 0, fmt.Errorf("mtd spend: %w", err)
	}

	fired := 0
	for _, alert := range alerts {
		if !ThresholdCrossed(mtd, budget.SoftCap, alert.ThresholdPct) {
			continue
		}
		if alreadyFiredThisPeriod(alert, period) {
			continue
		}

		if err := c.notifier.NotifySpendAlert(ctx, alert, wsID, mtd, budget.SoftCap); err != nil {
			c.logger.WarnContext(ctx, "spend alert dispatch failed",
				"alert_id", alert.ID, "workspace_id", wsID, "error", err)
			// Do NOT stamp last_fired_period on dispatch failure — retried next pass.
			continue
		}
		if err := c.repo.StampAlertFired(ctx, alert.ID, now, period); err != nil {
			c.logger.WarnContext(ctx, "spend alert stamp failed",
				"alert_id", alert.ID, "workspace_id", wsID, "error", err)
			continue
		}
		fired++
	}
	return fired, nil
}

// ThresholdCrossed reports whether mtd has crossed (soft_cap * pct / 100).
//
// All math via *big.Int — no float division. Computed as:
//
//	mtd * 100 >= soft_cap * pct
func ThresholdCrossed(mtd, softCap *big.Int, pct int) bool {
	if mtd == nil || softCap == nil {
		return false
	}
	if softCap.Sign() <= 0 {
		return false
	}
	if pct <= 0 {
		return false
	}

	lhs := new(big.Int).Mul(mtd, big.NewInt(100))
	rhs := new(big.Int).Mul(softCap, big.NewInt(int64(pct)))
	return lhs.Cmp(rhs) >= 0
}

// alreadyFiredThisPeriod reports whether the alert was already stamped for the
// given period (UTC month start).
func alreadyFiredThisPeriod(a SpendAlert, period time.Time) bool {
	if a.LastFiredPeriod == nil {
		return false
	}
	return a.LastFiredPeriod.Equal(period) || a.LastFiredPeriod.After(period)
}
