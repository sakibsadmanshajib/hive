package spendalerts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/budgets"
)

// =============================================================================
// Spend alert runner tests
// =============================================================================

// stubEval is a hand-rolled Evaluator for runner-loop tests.
type stubEval struct {
	mu       sync.Mutex
	calls    int
	fired    int
	err      error
	onCall   func(now time.Time)
}

func (s *stubEval) EvaluateBudgets(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.onCall != nil {
		s.onCall(now)
	}
	return s.fired, s.err
}

func (s *stubEval) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunner_RunOnce_DelegatesToEvaluator(t *testing.T) {
	t.Parallel()
	stub := &stubEval{fired: 3}
	r := NewRunner(stub, Config{Logger: quietLogger()})

	fired, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fired != 3 {
		t.Fatalf("fired=%d want 3", fired)
	}
	if stub.Calls() != 1 {
		t.Fatalf("calls=%d want 1", stub.Calls())
	}
}

func TestRunner_RunOnce_PropagatesEvaluatorError(t *testing.T) {
	t.Parallel()
	stub := &stubEval{err: errors.New("boom")}
	r := NewRunner(stub, Config{Logger: quietLogger()})

	_, err := r.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRunner_StartStop_TicksAtInterval(t *testing.T) {
	t.Parallel()
	stub := &stubEval{}
	r := NewRunner(stub, Config{Interval: 20 * time.Millisecond, Logger: quietLogger()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	time.Sleep(80 * time.Millisecond)
	r.Stop()

	calls := stub.Calls()
	// Eager first pass + at least 2 tick-driven passes within 80ms at 20ms interval.
	if calls < 2 {
		t.Fatalf("expected >=2 calls within window, got %d", calls)
	}
}

func TestRunner_Start_DoubleCallIsNoop(t *testing.T) {
	t.Parallel()
	stub := &stubEval{}
	r := NewRunner(stub, Config{Interval: 50 * time.Millisecond, Logger: quietLogger()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	r.Start(ctx) // should be ignored
	time.Sleep(10 * time.Millisecond)
	r.Stop()
	r.Stop() // double-stop also safe
}

// =============================================================================
// Threshold math + idempotency — exercise the budgets evaluator directly to
// satisfy Task 3b's "threshold math, idempotency, no-double-fire, soft-cap-only"
// matrix without taking a hard dep on the budgets package's internal layout.
// =============================================================================

// inMemoryRepo is a minimal WorkspaceBudgetRepository used by the cron tests.
type inMemoryRepo struct {
	budget    *budgets.Budget
	alerts    map[uuid.UUID]budgets.SpendAlert
	mtd       *big.Int
	stampedAt map[uuid.UUID]time.Time
}

func newInMemoryRepo() *inMemoryRepo {
	return &inMemoryRepo{
		alerts:    make(map[uuid.UUID]budgets.SpendAlert),
		stampedAt: make(map[uuid.UUID]time.Time),
	}
}

func (r *inMemoryRepo) GetBudget(_ context.Context, _ uuid.UUID) (*budgets.Budget, error) {
	if r.budget == nil {
		return nil, nil
	}
	cp := *r.budget
	if r.budget.SoftCap != nil {
		cp.SoftCap = new(big.Int).Set(r.budget.SoftCap)
	}
	if r.budget.HardCap != nil {
		cp.HardCap = new(big.Int).Set(r.budget.HardCap)
	}
	return &cp, nil
}

func (r *inMemoryRepo) UpsertBudget(_ context.Context, _ budgets.SetBudgetInput) (*budgets.Budget, error) {
	return nil, errors.New("not used")
}

func (r *inMemoryRepo) DeleteBudget(_ context.Context, _ uuid.UUID) error { return nil }

func (r *inMemoryRepo) ListAlerts(_ context.Context, _ uuid.UUID) ([]budgets.SpendAlert, error) {
	out := make([]budgets.SpendAlert, 0, len(r.alerts))
	for _, a := range r.alerts {
		out = append(out, a)
	}
	return out, nil
}

func (r *inMemoryRepo) CreateAlert(_ context.Context, _ budgets.CreateAlertInput) (*budgets.SpendAlert, error) {
	return nil, errors.New("not used")
}

func (r *inMemoryRepo) UpdateAlert(_ context.Context, _ budgets.UpdateAlertInput) (*budgets.SpendAlert, error) {
	return nil, errors.New("not used")
}

func (r *inMemoryRepo) DeleteAlert(_ context.Context, _ uuid.UUID) error { return nil }

func (r *inMemoryRepo) ListWorkspacesWithBudget(_ context.Context) ([]uuid.UUID, error) {
	if r.budget == nil {
		return nil, nil
	}
	return []uuid.UUID{r.budget.WorkspaceID}, nil
}

func (r *inMemoryRepo) StampAlertFired(_ context.Context, alertID uuid.UUID, firedAt time.Time, period time.Time) error {
	r.stampedAt[alertID] = firedAt
	a := r.alerts[alertID]
	a.LastFiredAt = &firedAt
	pp := period
	a.LastFiredPeriod = &pp
	r.alerts[alertID] = a
	return nil
}

func (r *inMemoryRepo) MonthToDateSpendBDT(_ context.Context, _ uuid.UUID, _ time.Time) (*big.Int, error) {
	if r.mtd == nil {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(r.mtd), nil
}

// recordingNotifier captures NotifySpendAlert calls.
type recordingNotifier struct {
	mu     sync.Mutex
	count  int32
	last   budgets.SpendAlert
	failOn int32 // alert call index to fail on (1-based); 0 disables failures
}

func (n *recordingNotifier) NotifySpendAlert(_ context.Context, alert budgets.SpendAlert, _ uuid.UUID, _, _ *big.Int) error {
	c := atomic.AddInt32(&n.count, 1)
	n.mu.Lock()
	n.last = alert
	n.mu.Unlock()
	if n.failOn != 0 && c == n.failOn {
		return errors.New("notifier failure")
	}
	return nil
}

func (n *recordingNotifier) Count() int {
	return int(atomic.LoadInt32(&n.count))
}

func newAlert(workspace uuid.UUID, pct int) budgets.SpendAlert {
	id := uuid.New()
	return budgets.SpendAlert{
		ID:           id,
		WorkspaceID:  workspace,
		ThresholdPct: pct,
	}
}

func budgetWith(softBDT, hardBDT int64) *budgets.Budget {
	return &budgets.Budget{
		WorkspaceID: uuid.New(),
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		SoftCap:     big.NewInt(softBDT),
		HardCap:     big.NewInt(hardBDT),
		Currency:    "BDT",
	}
}

func TestEvaluator_FiresAt50PercentThreshold(t *testing.T) {
	t.Parallel()
	repo := newInMemoryRepo()
	repo.budget = budgetWith(1000, 2000)
	repo.alerts[uuid.New()] = newAlert(repo.budget.WorkspaceID, 50)
	for id, a := range repo.alerts {
		a.ID = id
		repo.alerts[id] = a
	}
	repo.mtd = big.NewInt(500) // exactly 50%

	notifier := &recordingNotifier{}
	cron := budgets.NewCronEvaluator(repo, notifier, quietLogger())
	r := NewRunner(cron, Config{Logger: quietLogger()})

	fired, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fired != 1 {
		t.Fatalf("fired=%d want 1", fired)
	}
	if notifier.Count() != 1 {
		t.Fatalf("notifier count=%d want 1", notifier.Count())
	}
}

func TestEvaluator_NoFireBelowThreshold(t *testing.T) {
	t.Parallel()
	repo := newInMemoryRepo()
	repo.budget = budgetWith(1000, 2000)
	id := uuid.New()
	repo.alerts[id] = budgets.SpendAlert{ID: id, WorkspaceID: repo.budget.WorkspaceID, ThresholdPct: 50}
	repo.mtd = big.NewInt(499) // 49.9%

	notifier := &recordingNotifier{}
	cron := budgets.NewCronEvaluator(repo, notifier, quietLogger())
	r := NewRunner(cron, Config{Logger: quietLogger()})

	fired, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fired != 0 {
		t.Fatalf("fired=%d want 0", fired)
	}
}

func TestEvaluator_IdempotentWithinPeriod(t *testing.T) {
	t.Parallel()
	repo := newInMemoryRepo()
	repo.budget = budgetWith(1000, 2000)
	id := uuid.New()
	repo.alerts[id] = budgets.SpendAlert{ID: id, WorkspaceID: repo.budget.WorkspaceID, ThresholdPct: 80}
	repo.mtd = big.NewInt(800) // exactly 80%

	notifier := &recordingNotifier{}
	cron := budgets.NewCronEvaluator(repo, notifier, quietLogger())
	r := NewRunner(cron, Config{Logger: quietLogger()})

	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if notifier.Count() != 1 {
		t.Fatalf("notifier count=%d want 1 (alert must be one-shot per period)", notifier.Count())
	}
}

func TestEvaluator_MultipleThresholdsFireIndependently(t *testing.T) {
	t.Parallel()
	repo := newInMemoryRepo()
	repo.budget = budgetWith(1000, 2000)
	for _, pct := range []int{50, 80, 100} {
		id := uuid.New()
		repo.alerts[id] = budgets.SpendAlert{ID: id, WorkspaceID: repo.budget.WorkspaceID, ThresholdPct: pct}
	}
	repo.mtd = big.NewInt(1000) // 100%

	notifier := &recordingNotifier{}
	cron := budgets.NewCronEvaluator(repo, notifier, quietLogger())
	r := NewRunner(cron, Config{Logger: quietLogger()})

	fired, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fired != 3 {
		t.Fatalf("fired=%d want 3 (50/80/100 all crossed)", fired)
	}
}

func TestEvaluator_SoftCapZeroDisablesAlerts(t *testing.T) {
	t.Parallel()
	repo := newInMemoryRepo()
	repo.budget = budgetWith(0, 5000) // soft=0, hard=5000 ⇒ alerts disabled
	id := uuid.New()
	repo.alerts[id] = budgets.SpendAlert{ID: id, WorkspaceID: repo.budget.WorkspaceID, ThresholdPct: 50}
	repo.mtd = big.NewInt(2500)

	notifier := &recordingNotifier{}
	cron := budgets.NewCronEvaluator(repo, notifier, quietLogger())
	r := NewRunner(cron, Config{Logger: quietLogger()})

	fired, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fired != 0 {
		t.Fatalf("fired=%d want 0 (soft cap 0 must disable percentage alerts)", fired)
	}
}

func TestEvaluator_NotifierFailureDoesNotStamp(t *testing.T) {
	t.Parallel()
	repo := newInMemoryRepo()
	repo.budget = budgetWith(1000, 2000)
	id := uuid.New()
	repo.alerts[id] = budgets.SpendAlert{ID: id, WorkspaceID: repo.budget.WorkspaceID, ThresholdPct: 50}
	repo.mtd = big.NewInt(500)

	// Fail on first call, succeed on second (simulates retry on next pass).
	notifier := &recordingNotifier{failOn: 1}
	cron := budgets.NewCronEvaluator(repo, notifier, quietLogger())
	r := NewRunner(cron, Config{Logger: quietLogger()})

	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// First pass dispatch failed → not stamped → second pass should re-fire.
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if notifier.Count() != 2 {
		t.Fatalf("notifier count=%d want 2 (failed dispatch must be retried next pass)", notifier.Count())
	}
}
