package budgets_test

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
)

// =============================================================================
// The alert pass is what makes the hard cap survive an ordinary day (#1651)
//
// The settlement counter alone leaves two holes, both raised by the independent
// money review on PR #1677 and neither exotic:
//
//   - A counter write that fails while its key is alive is not a missing key, so
//     nothing rebuilds and that charge is gone from the cap for the month.
//   - Redis in this deployment has no volume, so every deploy starts empty and
//     every cap key is gone until something republishes it.
//
// Both are closed by restating the values on the one schedule that already
// walks every workspace with a budget. These tests pin that it runs, and that it
// runs for workspaces the alert logic itself would have skipped.
// =============================================================================

type syncCall struct {
	workspaceID uuid.UUID
	credits     *big.Int
	hardCap     *big.Int
}

type fakeGateSync struct {
	mu    sync.Mutex
	calls []syncCall
	err   error
}

func (f *fakeGateSync) SyncWorkspace(_ context.Context, ws uuid.UUID, credits, hardCap *big.Int, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, syncCall{workspaceID: ws, credits: credits, hardCap: hardCap})
	return f.err
}

func (f *fakeGateSync) seen() []syncCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]syncCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func seedBudget(t *testing.T, repo *fakeWorkspaceRepo, ws uuid.UUID, soft, hard int64, period time.Time) {
	t.Helper()
	if _, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: ws,
		PeriodStart: period,
		SoftCap:     big.NewInt(soft),
		HardCap:     big.NewInt(hard),
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
}

// TestEvaluateBudgets_SyncsTheGateForWorkspacesWithNoAlerts is the important
// one. A hard cap is enforced whether or not the customer configured a soft cap
// or any alert, so the sync has to happen before the alert-shaped early returns,
// not after them. Put it after either and every workspace that only set a hard
// cap keeps a stale or absent counter forever.
func TestEvaluateBudgets_SyncsTheGateForWorkspacesWithNoAlerts(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	gate := &fakeGateSync{}
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	noSoftCap := uuid.New()
	noAlerts := uuid.New()
	seedBudget(t, repo, noSoftCap, 0, 200_000, period)
	seedBudget(t, repo, noAlerts, 100_000, 200_000, period)
	repo.mtd[noSoftCap] = spendCredits(5_000)
	repo.mtd[noAlerts] = spendCredits(7_000)

	cron := budgets.NewCronEvaluator(repo, &fakeAlertNotifier{}, nil).WithGateStateSync(gate)
	if _, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	calls := gate.seen()
	if len(calls) != 2 {
		t.Fatalf("expected both workspaces synced, got %#v", calls)
	}
	for _, call := range calls {
		if call.hardCap == nil || call.hardCap.Cmp(big.NewInt(200_000)) != 0 {
			t.Fatalf("workspace %s synced with cap %v, want 200000", call.workspaceID, call.hardCap)
		}
		want := spendCredits(5_000)
		if call.workspaceID == noAlerts {
			want = spendCredits(7_000)
		}
		if call.credits == nil || call.credits.Cmp(want) != 0 {
			t.Fatalf("workspace %s synced with %v credits, want %v", call.workspaceID, call.credits, want)
		}
	}
}

// TestEvaluateBudgets_SyncFailureDoesNotStopTheAlerts keeps the two controls
// independent. A Redis that will not take the sync must not silently cost a
// customer the spend warning they configured, which arrives by mail and does not
// need Redis at all.
func TestEvaluateBudgets_SyncFailureDoesNotStopTheAlerts(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	notifier := &fakeAlertNotifier{}
	gate := &fakeGateSync{err: errors.New("redis is down")}
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	ws := uuid.New()
	seedBudget(t, repo, ws, 100_000, 200_000, period)
	if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
		WorkspaceID:  ws,
		ThresholdPct: 50,
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	repo.mtd[ws] = spendCredits(50_000) // half the soft cap exactly

	cron := budgets.NewCronEvaluator(repo, notifier, nil).WithGateStateSync(gate)
	fired, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if fired != 1 {
		t.Fatalf("a failed gate sync suppressed the alert: fired %d", fired)
	}
}

// TestEvaluateBudgets_WithoutAGateSyncStillRuns keeps the seam optional, which
// is what a deployment with no Redis gets.
func TestEvaluateBudgets_WithoutAGateSyncStillRuns(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ws := uuid.New()
	seedBudget(t, repo, ws, 100_000, 200_000, period)
	repo.mtd[ws] = spendCredits(5_000)

	cron := budgets.NewCronEvaluator(repo, &fakeAlertNotifier{}, nil)
	if _, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("evaluate with no gate sync wired: %v", err)
	}
}
