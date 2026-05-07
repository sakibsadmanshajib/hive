package budgets_test

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/budgets"
)

// =============================================================================
// Phase 14 — workspace budget + spend-alert + cron tests
// =============================================================================

// fakeWorkspaceRepo implements WorkspaceBudgetRepository in memory.
type fakeWorkspaceRepo struct {
	mu      sync.Mutex
	budgets map[uuid.UUID]*budgets.Budget
	alerts  map[uuid.UUID][]budgets.SpendAlert
	mtd     map[uuid.UUID]*big.Int
}

func newFakeWorkspaceRepo() *fakeWorkspaceRepo {
	return &fakeWorkspaceRepo{
		budgets: make(map[uuid.UUID]*budgets.Budget),
		alerts:  make(map[uuid.UUID][]budgets.SpendAlert),
		mtd:     make(map[uuid.UUID]*big.Int),
	}
}

func (f *fakeWorkspaceRepo) GetBudget(_ context.Context, ws uuid.UUID) (*budgets.Budget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.budgets[ws]
	if !ok {
		return nil, nil
	}
	cp := *b
	cp.SoftCap = new(big.Int).Set(b.SoftCap)
	cp.HardCap = new(big.Int).Set(b.HardCap)
	return &cp, nil
}

func (f *fakeWorkspaceRepo) UpsertBudget(_ context.Context, in budgets.SetBudgetInput) (*budgets.Budget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	b := &budgets.Budget{
		WorkspaceID: in.WorkspaceID,
		PeriodStart: in.PeriodStart,
		SoftCap:     new(big.Int).Set(in.SoftCap),
		HardCap:     new(big.Int).Set(in.HardCap),
		Currency:    "BDT",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	f.budgets[in.WorkspaceID] = b
	return b, nil
}

func (f *fakeWorkspaceRepo) DeleteBudget(_ context.Context, ws uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.budgets, ws)
	return nil
}

func (f *fakeWorkspaceRepo) ListAlerts(_ context.Context, ws uuid.UUID) ([]budgets.SpendAlert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]budgets.SpendAlert, len(f.alerts[ws]))
	copy(out, f.alerts[ws])
	return out, nil
}

func (f *fakeWorkspaceRepo) CreateAlert(_ context.Context, in budgets.CreateAlertInput) (*budgets.SpendAlert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := budgets.SpendAlert{
		ID:            uuid.New(),
		WorkspaceID:   in.WorkspaceID,
		ThresholdPct:  in.ThresholdPct,
		Email:         in.Email,
		WebhookURL:    in.WebhookURL,
		WebhookSecret: in.WebhookSecret,
		CreatedAt:     time.Now().UTC(),
	}
	f.alerts[in.WorkspaceID] = append(f.alerts[in.WorkspaceID], a)
	return &a, nil
}

func (f *fakeWorkspaceRepo) UpdateAlert(_ context.Context, in budgets.UpdateAlertInput) (*budgets.SpendAlert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ws, list := range f.alerts {
		for i, a := range list {
			if a.ID == in.ID {
				if in.Email != nil {
					f.alerts[ws][i].Email = in.Email
				}
				if in.WebhookURL != nil {
					f.alerts[ws][i].WebhookURL = in.WebhookURL
				}
				if in.WebhookSecret != nil {
					f.alerts[ws][i].WebhookSecret = in.WebhookSecret
				}
				updated := f.alerts[ws][i]
				return &updated, nil
			}
		}
	}
	return nil, budgets.ErrBudgetNotFound
}

func (f *fakeWorkspaceRepo) DeleteAlert(_ context.Context, alertID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ws, list := range f.alerts {
		for i, a := range list {
			if a.ID == alertID {
				f.alerts[ws] = append(list[:i], list[i+1:]...)
				return nil
			}
		}
	}
	return nil
}

func (f *fakeWorkspaceRepo) ListWorkspacesWithBudget(_ context.Context) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uuid.UUID, 0, len(f.budgets))
	for k := range f.budgets {
		out = append(out, k)
	}
	return out, nil
}

func (f *fakeWorkspaceRepo) StampAlertFired(_ context.Context, alertID uuid.UUID, firedAt time.Time, period time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ws, list := range f.alerts {
		for i, a := range list {
			if a.ID == alertID {
				ft := firedAt
				p := period
				f.alerts[ws][i].LastFiredAt = &ft
				f.alerts[ws][i].LastFiredPeriod = &p
				return nil
			}
		}
	}
	return errors.New("alert not found")
}

func (f *fakeWorkspaceRepo) MonthToDateSpendBDT(_ context.Context, ws uuid.UUID, _ time.Time) (*big.Int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.mtd[ws]
	if !ok {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(v), nil
}

// fakeAlertNotifier counts calls.
type fakeAlertNotifier struct {
	mu      sync.Mutex
	calls   int
	lastID  uuid.UUID
	failure error
}

func (n *fakeAlertNotifier) NotifySpendAlert(_ context.Context, a budgets.SpendAlert, _ uuid.UUID, _, _ *big.Int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	n.lastID = a.ID
	return n.failure
}

func newWorkspaceService(repo budgets.WorkspaceBudgetRepository, notifier budgets.AlertNotifier) *budgets.Service {
	// Legacy threshold path stays nil — workspace tests don't exercise it.
	return budgets.NewServiceWithWorkspace(&legacyNoopRepo{}, &legacyNoopNotifier{}, repo, notifier, nil)
}

type legacyNoopRepo struct{}

func (*legacyNoopRepo) GetThreshold(_ context.Context, _ uuid.UUID) (*budgets.BudgetThreshold, error) {
	return nil, nil
}
func (*legacyNoopRepo) UpsertThreshold(_ context.Context, _ uuid.UUID, _ int64) (*budgets.BudgetThreshold, error) {
	return nil, nil
}
func (*legacyNoopRepo) DismissAlert(_ context.Context, _ uuid.UUID) error  { return nil }
func (*legacyNoopRepo) MarkNotified(_ context.Context, _ uuid.UUID) error { return nil }

type legacyNoopNotifier struct{}

func (*legacyNoopNotifier) SendBudgetAlert(_ context.Context, _ uuid.UUID, _ budgets.BudgetThreshold, _ int64) error {
	return nil
}

// -----------------------------------------------------------------------------
// SetBudget validation
// -----------------------------------------------------------------------------

func TestSetBudget_HardLessThanSoft_Returns_ErrInvalidCaps(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc := newWorkspaceService(repo, &fakeAlertNotifier{})
	_, err := svc.SetBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: uuid.New(),
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		SoftCap:     big.NewInt(2000_00),
		HardCap:     big.NewInt(1000_00),
	})
	if !errors.Is(err, budgets.ErrInvalidCaps) {
		t.Fatalf("expected ErrInvalidCaps, got %v", err)
	}
}

func TestSetBudget_PersistsAndRoundTrips(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc := newWorkspaceService(repo, &fakeAlertNotifier{})
	wsID := uuid.New()
	soft := big.NewInt(1000_00)
	hard := big.NewInt(2000_00)
	_, err := svc.SetBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: wsID,
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		SoftCap:     soft,
		HardCap:     hard,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	got, err := svc.GetBudget(context.Background(), wsID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SoftCap.Cmp(soft) != 0 || got.HardCap.Cmp(hard) != 0 {
		t.Fatalf("round-trip mismatch: got soft=%s hard=%s", got.SoftCap.String(), got.HardCap.String())
	}
}

func TestSetBudget_NegativeCap_Rejected(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc := newWorkspaceService(repo, &fakeAlertNotifier{})
	_, err := svc.SetBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: uuid.New(),
		PeriodStart: time.Now().UTC(),
		SoftCap:     big.NewInt(-1),
		HardCap:     big.NewInt(1000),
	})
	if !errors.Is(err, budgets.ErrInvalidCaps) {
		t.Fatalf("expected ErrInvalidCaps, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Spend alert CRUD validation
// -----------------------------------------------------------------------------

func TestCreateAlert_AcceptsValidThresholds(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc := newWorkspaceService(repo, &fakeAlertNotifier{})
	wsID := uuid.New()
	for _, pct := range []int{50, 80, 100} {
		_, err := svc.CreateAlert(context.Background(), budgets.CreateAlertInput{
			WorkspaceID:  wsID,
			ThresholdPct: pct,
		})
		if err != nil {
			t.Fatalf("unexpected for pct=%d: %v", pct, err)
		}
	}
	alerts, err := svc.ListAlerts(context.Background(), wsID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(alerts) != 3 {
		t.Fatalf("expected 3 alerts, got %d", len(alerts))
	}
}

func TestCreateAlert_RejectsInvalidThreshold(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc := newWorkspaceService(repo, &fakeAlertNotifier{})
	_, err := svc.CreateAlert(context.Background(), budgets.CreateAlertInput{
		WorkspaceID:  uuid.New(),
		ThresholdPct: 75,
	})
	if !errors.Is(err, budgets.ErrInvalidThreshold) {
		t.Fatalf("expected ErrInvalidThreshold, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// ThresholdCrossed math (no float division)
// -----------------------------------------------------------------------------

func TestThresholdCrossed_BoundaryAndOverflow(t *testing.T) {
	tests := []struct {
		name    string
		mtd     *big.Int
		softCap *big.Int
		pct     int
		want    bool
	}{
		{"below 50pct", big.NewInt(499), big.NewInt(1000), 50, false},
		{"at 50pct exact", big.NewInt(500), big.NewInt(1000), 50, true},
		{"above 80pct", big.NewInt(801), big.NewInt(1000), 80, true},
		{"at 100pct", big.NewInt(1000), big.NewInt(1000), 100, true},
		// Overflow check: values larger than int64 still compare correctly via big.Int.
		{"big overflow", new(big.Int).Mul(big.NewInt(1<<60), big.NewInt(2)), new(big.Int).Mul(big.NewInt(1<<60), big.NewInt(3)), 50, true},
		{"zero soft", big.NewInt(100), big.NewInt(0), 50, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := budgets.ThresholdCrossed(tc.mtd, tc.softCap, tc.pct)
			if got != tc.want {
				t.Fatalf("ThresholdCrossed(%s, %s, %d) = %v, want %v",
					tc.mtd.String(), tc.softCap.String(), tc.pct, got, tc.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Cron evaluator — idempotency + period rollover
// -----------------------------------------------------------------------------

func TestEvaluateBudgets_FiresOncePerPeriod(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	notifier := &fakeAlertNotifier{}
	wsID := uuid.New()
	period := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	if _, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: wsID,
		PeriodStart: period,
		SoftCap:     big.NewInt(1000),
		HardCap:     big.NewInt(2000),
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
		WorkspaceID:  wsID,
		ThresholdPct: 50,
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	repo.mtd[wsID] = big.NewInt(500) // 50% exactly

	cron := budgets.NewCronEvaluator(repo, notifier, nil)

	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	fired, err := cron.EvaluateBudgets(context.Background(), now)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if fired != 1 {
		t.Fatalf("expected 1 fired, got %d", fired)
	}

	// Second pass within same period MUST not re-fire.
	now2 := now.Add(1 * time.Minute)
	fired2, err := cron.EvaluateBudgets(context.Background(), now2)
	if err != nil {
		t.Fatalf("evaluate2: %v", err)
	}
	if fired2 != 0 {
		t.Fatalf("expected 0 on idempotent re-pass, got %d", fired2)
	}
}

func TestEvaluateBudgets_RefiresOnNextPeriod(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	notifier := &fakeAlertNotifier{}
	wsID := uuid.New()
	periodApr := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	if _, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: wsID,
		PeriodStart: periodApr,
		SoftCap:     big.NewInt(1000),
		HardCap:     big.NewInt(2000),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
		WorkspaceID:  wsID,
		ThresholdPct: 50,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	repo.mtd[wsID] = big.NewInt(500)

	cron := budgets.NewCronEvaluator(repo, notifier, nil)

	if _, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("apr eval: %v", err)
	}
	// New period (May) should refire.
	mayNow := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	fired, err := cron.EvaluateBudgets(context.Background(), mayNow)
	if err != nil {
		t.Fatalf("may eval: %v", err)
	}
	if fired != 1 {
		t.Fatalf("expected refire in new period, got %d", fired)
	}
}
