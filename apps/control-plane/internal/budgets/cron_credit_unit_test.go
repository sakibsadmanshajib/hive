package budgets_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
)

// TestEvaluateBudgets_ComparesTakaCapAgainstConvertedCredits is the spend-alert
// half of issue #1648.
//
// The ledger stores credits (1 USD = 1,000,000,000 of them) and the customer
// types a soft cap in taka on /console/billing/budget. The cron used to compare
// the two integers directly, so a workspace that had spent half a dollar looked
// like it had blown through a one thousand taka cap and every threshold fired.
//
// Magnitudes are the live ones measured on 2026-09-01: 524,653,338 credits of
// August usage, about 0.52 USD, about 52 taka at 100 BDT per USD. Against a
// 1,000 taka soft cap that is five percent, which crosses no threshold. The
// unconverted comparison is 524,653,338 against 100,000 subunits and fires all
// three.
func TestEvaluateBudgets_ComparesTakaCapAgainstConvertedCredits(t *testing.T) {
	t.Setenv("HIVE_USD_BDT_RATE", "100")

	repo := newFakeWorkspaceRepo()
	notifier := &fakeAlertNotifier{}
	wsID := uuid.New()
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	if _, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: wsID,
		PeriodStart: period,
		SoftCap:     big.NewInt(1000_00), // ৳1,000
		HardCap:     big.NewInt(2000_00), // ৳2,000
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	for _, pct := range []int{50, 80, 100} {
		if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
			WorkspaceID:  wsID,
			ThresholdPct: pct,
		}); err != nil {
			t.Fatalf("seed alert %d: %v", pct, err)
		}
	}

	repo.mtd[wsID] = big.NewInt(524_653_338) // credits, about 0.52 USD

	cron := budgets.NewCronEvaluator(repo, notifier, nil)
	fired, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if fired != 0 {
		t.Fatalf("fired %d alerts on about ৳52 of spend against a ৳1,000 cap; credits are being compared as subunits", fired)
	}
}

// TestEvaluateBudgets_FiresOnceSpendReallyReachesTheCap is the other half: the
// conversion must not silence a genuine crossing. 500,000,000,000 credits is
// 500 USD, which is ৳50,000 at the rate below, exactly half of a ৳100,000 cap.
func TestEvaluateBudgets_FiresOnceSpendReallyReachesTheCap(t *testing.T) {
	t.Setenv("HIVE_USD_BDT_RATE", "100")

	repo := newFakeWorkspaceRepo()
	notifier := &fakeAlertNotifier{}
	wsID := uuid.New()
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	if _, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: wsID,
		PeriodStart: period,
		SoftCap:     big.NewInt(100_000_00), // ৳100,000
		HardCap:     big.NewInt(200_000_00),
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
		WorkspaceID:  wsID,
		ThresholdPct: 50,
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	repo.mtd[wsID] = new(big.Int).Mul(big.NewInt(500), big.NewInt(1_000_000_000)) // 500 USD

	cron := budgets.NewCronEvaluator(repo, notifier, nil)
	fired, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if fired != 1 {
		t.Fatalf("fired %d alerts, want 1: ৳50,000 of spend is exactly half of the ৳100,000 cap", fired)
	}
}

// TestEvaluateBudgets_RefusesAnUnusableRate keeps the pass fail-closed. A
// misconfigured rate must stop the evaluation, not fall back to a rate nobody
// chose and mail customers numbers derived from it.
func TestEvaluateBudgets_RefusesAnUnusableRate(t *testing.T) {
	t.Setenv("HIVE_USD_BDT_RATE", "not-a-rate")

	repo := newFakeWorkspaceRepo()
	notifier := &fakeAlertNotifier{}
	wsID := uuid.New()
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	if _, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: wsID,
		PeriodStart: period,
		SoftCap:     big.NewInt(1000_00),
		HardCap:     big.NewInt(2000_00),
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
		WorkspaceID:  wsID,
		ThresholdPct: 50,
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	repo.mtd[wsID] = new(big.Int).Mul(big.NewInt(500), big.NewInt(1_000_000_000))

	cron := budgets.NewCronEvaluator(repo, notifier, nil)
	if _, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected the pass to refuse an unparseable rate")
	}
	if notifier.calls != 0 {
		t.Fatalf("notifier called %d times despite an unusable rate", notifier.calls)
	}
}
