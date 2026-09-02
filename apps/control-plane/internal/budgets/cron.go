package budgets

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// =============================================================================
// Phase 14 — Spend alert cron evaluator.
//
// Walks workspaces with active budgets, reads month-to-date spend in ledger
// credits, converts it to BDT subunits at the platform rate (math/big),
// compares the result against each configured alert threshold, and
// fires the notifier exactly once per (alert, period). Idempotency is enforced
// by stamping last_fired_period in the spend_alerts row.
//
// Threshold cross math (no float division):
//
//	mtd * 100 >= soft_cap * threshold_pct
//
// All operands are *big.Int; the inequality uses Cmp on a temporary big.Int.
// =============================================================================

// GateStateSyncer restates the Redis values the edge-api budget gate reads for
// one workspace, from figures already read out of the database.
//
// The pass below is the only thing in this product that walks every workspace
// with a budget on a schedule, so it is where the gate's view converges on the
// ledger: see MTDSpendCounter.SyncWorkspace for the two holes that closes.
// Optional, and nil in any deployment without Redis.
type GateStateSyncer interface {
	SyncWorkspace(ctx context.Context, workspaceID uuid.UUID, ledgerCredits, hardCap *big.Int, at time.Time) error
}

// CronEvaluator owns the spend-alert evaluation pass.
type CronEvaluator struct {
	repo     WorkspaceBudgetRepository
	notifier AlertNotifier
	logger   *slog.Logger
	now      func() time.Time
	gate     GateStateSyncer
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

// WithGateStateSync makes each pass also restate the edge-api gate's Redis view
// of every workspace it visits. Returns the evaluator for chaining. Without it
// the pass only sends alerts, which is what a deployment with no Redis gets.
func (c *CronEvaluator) WithGateStateSync(gate GateStateSyncer) *CronEvaluator {
	if gate != nil {
		c.gate = gate
	}
	return c
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

	// The caps are taka; the ledger is credits. Resolve the rate once for the
	// whole pass so every workspace in it is measured against the same number,
	// and refuse the pass outright if the rate is unusable rather than mail
	// customers thresholds derived from a rate nobody configured (issue #1648).
	//
	// The platform rate, deliberately, and not each account's own FX snapshot
	// the way an invoice does. An invoice is a document that has to reconcile
	// against the receipt for the top-up that funded it; a soft-cap alert is a
	// threshold heuristic on a mid-month running total, and one rate per pass
	// keeps every workspace in that pass mutually comparable, which per-account
	// snapshots would not.
	//
	// The gap is small but NOT symmetric, and the direction is the part worth
	// knowing. A snapshot rate is the mid rate times payments.FXFeeRate, so it
	// is about five percent higher than the platform rate, so the same credits
	// convert to about five percent FEWER taka here than they will on the
	// invoice. The alert therefore fires slightly LATER than the invoice's
	// arithmetic implies, and late is the direction that serves the customer
	// least on a spend warning. Small enough not to change the design over;
	// stated so that whoever decides to close the gap knows what closing it
	// buys.
	rate, err := payments.PlatformUSDBDTRate()
	if err != nil {
		return 0, fmt.Errorf("budgets: usd to bdt rate: %w", err)
	}
	c.logger.InfoContext(ctx, "budget alert pass: usd to bdt rate resolved",
		"rate", rate.Display, "source", rate.Source)

	workspaceIDs, err := c.repo.ListWorkspacesWithBudget(ctx)
	if err != nil {
		return 0, fmt.Errorf("budgets: list workspaces with budget: %w", err)
	}

	fired := 0
	for _, wsID := range workspaceIDs {
		n, err := c.evaluateWorkspace(ctx, wsID, now, period, rate)
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

func (c *CronEvaluator) evaluateWorkspace(ctx context.Context, wsID uuid.UUID, now, period time.Time, rate payments.USDBDTRate) (int, error) {
	budget, err := c.repo.GetBudget(ctx, wsID)
	if err != nil {
		return 0, fmt.Errorf("get budget: %w", err)
	}
	if budget == nil {
		return 0, nil
	}

	// Read the ledger before the alert-shaped early returns below, because the
	// gate sync needs it for every workspace with a budget, including one with
	// no soft cap and one with no alerts configured. Those workspaces still have
	// a hard cap that has to be enforced. One indexed aggregate per capped
	// workspace per pass.
	mtdCredits, err := c.repo.MonthToDateSpendCredits(ctx, wsID, period)
	if err != nil {
		return 0, fmt.Errorf("mtd spend: %w", err)
	}

	// Convert before comparing: the cap the customer typed is in taka and the
	// ledger total is in credits, one billionth of a USD each.
	mtd, err := payments.CreditsToBDTSubunits(mtdCredits, rate.Rate)
	if err != nil {
		return 0, fmt.Errorf("mtd spend conversion: %w", err)
	}

	// Restate what the edge gate reads. This is the only scheduled walk of every
	// workspace with a budget, so it is what puts caps back after a deploy
	// empties Redis, and what corrects a counter write that failed while its key
	// was alive. Failure here is logged and does not stop the alert it shares a
	// pass with: the two are independent controls.
	if c.gate != nil {
		if err := c.gate.SyncWorkspace(ctx, wsID, mtdCredits, budget.HardCap, now); err != nil {
			c.logger.ErrorContext(ctx, "budget gate state sync failed; this workspace's hard cap may be unenforced",
				"workspace_id", wsID, "error", err)
		}
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
