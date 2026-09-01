package budgets_test

import (
	"context"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
)

// Behavioral coverage for the budgets money surface against the real Postgres
// schema (HIVE_TEST_DB_URL gated, same convention as the accounting suites).
//
// Invariants encoded here:
//
//   - Re-raising a threshold re-arms alerts: upsert clears alert_dismissed, so
//     an operator who changes the limit is notified again.
//   - A balance exactly at the threshold is a breach; strictly above is not.
//   - A failed notification must not stamp the alert as sent (fail-closed
//     retry: the next pass retries instead of silently dropping the alert),
//     both for the legacy evaluator and the spend-alert cron.
//   - Alert mutation is tenant-isolated at the data layer: another workspace
//     cannot mutate or delete an alert it does not own even with its UUID.
//   - Spend aggregation sums only usage_charge rows inside [periodStart, now]
//     and can never report negative spend.
//   - One workspace's evaluation failure never blocks another's alerts.

func newBudgetsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		// CI wires HIVE_TEST_DB_URL for the live-Postgres step and passes
		// -short for the step that has none, so a missing DSN there is a wiring
		// defect (the silent-green never-runs shape of issues #701/#708/#797),
		// not a laptop without Postgres. Fail loudly in CI live leg; local runs
		// without a test database still skip.
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatal("HIVE_TEST_DB_URL not set in CI: this suite guards a real-SQL proof and must not silently skip")
		}
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedBudgetWorkspace creates one auth user plus one personal account (the
// Phase 14 workspace entity IS public.accounts) and registers cleanup. The
// account delete cascades budgets/spend_alerts/account_budget_thresholds and
// credit_ledger_entries rows created by each test.
func seedBudgetWorkspace(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID, accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"budgets-ws-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		t.Fatalf("seed auth.users failed (is this a migrated test DB?): %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (slug, display_name, account_type, owner_user_id)
		 VALUES ($1, 'budgets ws test', 'personal', $2) RETURNING id`,
		"budgets-ws-"+uuid.NewString(), userID,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth.users WHERE id = $1`, userID)
	})
	return accountID
}

func newLiveWorkspaceService(pool *pgxpool.Pool) *budgets.Service {
	return budgets.NewServiceWithWorkspace(
		&legacyNoopRepo{}, &legacyNoopNotifier{},
		budgets.NewWorkspacePgxRepository(pool), &fakeAlertNotifier{}, nil,
	)
}

// failingEmailNotifier always errors, for the non-fatal-notification branch.
type failingEmailNotifier struct{ called bool }

func (n *failingEmailNotifier) SendBudgetAlert(context.Context, uuid.UUID, budgets.BudgetThreshold, int64) error {
	n.called = true
	return errors.New("smtp down")
}

func seedCharge(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, delta int64, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.credit_ledger_entries (account_id, entry_type, credits_delta, idempotency_key, request_id, created_at)
		 VALUES ($1, 'usage_charge', $2, $3, 'budgets-mtd', now() - $4::interval)`,
		accountID, delta, "mtd-"+uuid.NewString(), age,
	); err != nil {
		t.Fatalf("seed charge: %v", err)
	}
}

// TestThresholdLifecycle_Live walks the legacy account-threshold surface end to
// end: upsert, dismiss, re-arm on re-upsert, mark-notified.
func TestThresholdLifecycle_Live(t *testing.T) {
	pool := newBudgetsTestPool(t)
	accountID := seedBudgetWorkspace(t, pool)
	ctx := context.Background()
	repo := budgets.NewPgxRepository(pool)

	upserted, err := repo.UpsertThreshold(ctx, accountID, 50000)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upserted.ThresholdCredits != 50000 || upserted.AlertDismissed {
		t.Fatalf("fresh threshold wrong: %+v", upserted)
	}

	if err := repo.DismissAlert(ctx, accountID); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	got, err := repo.GetThreshold(ctx, accountID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || !got.AlertDismissed {
		t.Fatalf("dismiss did not persist: %+v", got)
	}

	// Invariant: updating the threshold re-arms the alert.
	if _, err := repo.UpsertThreshold(ctx, accountID, 60000); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	armed, err := repo.GetThreshold(ctx, accountID)
	if err != nil {
		t.Fatalf("get after re-upsert: %v", err)
	}
	if armed == nil || armed.AlertDismissed {
		t.Fatalf("re-upsert did not re-arm alerts: %+v", armed)
	}
	if armed.ThresholdCredits != 60000 {
		t.Fatalf("threshold value not updated: %+v", armed)
	}

	if err := repo.MarkNotified(ctx, accountID); err != nil {
		t.Fatalf("mark notified: %v", err)
	}
	notified, err := repo.GetThreshold(ctx, accountID)
	if err != nil {
		t.Fatalf("get after mark: %v", err)
	}
	if notified.LastNotifiedAt == nil {
		t.Fatal("MarkNotified did not stamp last_notified_at")
	}
}

// TestCheckThresholdsEdges pins legacy-evaluator decisions beyond the existing
// suite: exact-at-limit is a breach and stamps MarkNotified; a notifier failure
// stays non-fatal and never stamps; cooldown expiry re-notifies.
func TestCheckThresholdsEdges(t *testing.T) {
	newBreachRepo := func() (*mockRepo, uuid.UUID) {
		acct := uuid.New()
		return &mockRepo{threshold: &budgets.BudgetThreshold{
			ID: uuid.New(), AccountID: acct, ThresholdCredits: 100000,
		}}, acct
	}

	t.Run("at exactly the threshold is a breach and marks notified", func(t *testing.T) {
		repo, acct := newBreachRepo()
		notifier := &mockNotifier{}
		svc := budgets.NewService(repo, notifier)

		if err := svc.CheckThresholds(context.Background(), acct, 100000); err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !notifier.called {
			t.Fatal("balance == threshold must be treated as breached")
		}
		if !repo.notified {
			t.Fatal("successful send must stamp MarkNotified")
		}
	})

	t.Run("failed notification is non-fatal and never stamps sent", func(t *testing.T) {
		repo, acct := newBreachRepo()
		failer := &failingEmailNotifier{}
		svc := budgets.NewService(repo, failer)

		if err := svc.CheckThresholds(context.Background(), acct, 100); err != nil {
			t.Fatalf("notification failure must not fail the caller, got %v", err)
		}
		if !failer.called {
			t.Fatal("notifier should still have been attempted")
		}
		if repo.notified {
			t.Fatal("a failed send must not be stamped as notified; the next pass must retry")
		}
	})

	t.Run("cooldown expiry re-notifies", func(t *testing.T) {
		repo, acct := newBreachRepo()
		stale := time.Now().Add(-25 * time.Hour)
		repo.threshold.LastNotifiedAt = &stale
		notifier := &mockNotifier{}
		svc := budgets.NewService(repo, notifier)

		if err := svc.CheckThresholds(context.Background(), acct, 100); err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !notifier.called {
			t.Fatal("after the 24h cooldown a standing breach must notify again")
		}
	})
}

// TestWorkspaceBudgetRoundTrip_Live covers SetBudget/GetBudget/DeleteBudget
// against real rows, including zero PeriodStart defaulting to the current
// month start and big.Int round-tripping through the bigint columns.
func TestWorkspaceBudgetRoundTrip_Live(t *testing.T) {
	pool := newBudgetsTestPool(t)
	wsID := seedBudgetWorkspace(t, pool)
	ctx := context.Background()
	svc := newLiveWorkspaceService(pool)

	b, err := svc.SetBudget(ctx, budgets.SetBudgetInput{
		WorkspaceID: wsID,
		SoftCap:     big.NewInt(50_000),
		HardCap:     big.NewInt(100_000),
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if b.Currency != "BDT" {
		t.Fatalf("currency %q, want BDT", b.Currency)
	}
	if b.PeriodStart.IsZero() {
		t.Fatal("zero PeriodStart must default to the current month start, not stay zero")
	}

	got, err := svc.GetBudget(ctx, wsID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.SoftCap.Cmp(big.NewInt(50_000)) != 0 || got.HardCap.Cmp(big.NewInt(100_000)) != 0 {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	lower, err := svc.SetBudget(ctx, budgets.SetBudgetInput{
		WorkspaceID: wsID,
		SoftCap:     big.NewInt(10_000),
		HardCap:     big.NewInt(20_000),
	})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if lower.HardCap.Cmp(big.NewInt(20_000)) != 0 {
		t.Fatalf("upsert did not overwrite hard cap: %s", lower.HardCap.String())
	}

	if err := svc.DeleteBudget(ctx, wsID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, err := svc.GetBudget(ctx, wsID)
	if err != nil || gone != nil {
		t.Fatalf("deleted budget resolved to (%v, %v), want (nil, nil)", gone, err)
	}

	if _, err := svc.SetBudget(ctx, budgets.SetBudgetInput{
		WorkspaceID: wsID,
		SoftCap:     big.NewInt(30_000),
		HardCap:     big.NewInt(10_000),
	}); !errors.Is(err, budgets.ErrInvalidCaps) {
		t.Fatalf("hard<soft returned %v, want ErrInvalidCaps", err)
	}
	if _, err := svc.SetBudget(ctx, budgets.SetBudgetInput{
		WorkspaceID: wsID,
	}); !errors.Is(err, budgets.ErrInvalidCaps) {
		t.Fatalf("missing caps returned %v, want ErrInvalidCaps", err)
	}
}

// TestSpendAlertCRUDAndIsolation_Live covers alert create/list/update/delete
// plus the tenant-isolation contract: another workspace cannot mutate or
// delete an alert it does not own even with the right UUID.
func TestSpendAlertCRUDAndIsolation_Live(t *testing.T) {
	pool := newBudgetsTestPool(t)
	wsA := seedBudgetWorkspace(t, pool)
	wsB := seedBudgetWorkspace(t, pool)
	ctx := context.Background()
	svc := newLiveWorkspaceService(pool)

	firstEmail := "ops-" + uuid.NewString()[:8] + "@test.local"
	inputs := []struct {
		pct   int
		email *string
	}{
		{80, &firstEmail},
		{50, nil},
	}
	for _, in := range inputs {
		if _, err := svc.CreateAlert(ctx, budgets.CreateAlertInput{
			WorkspaceID: wsA, ThresholdPct: in.pct, Email: in.email,
		}); err != nil {
			t.Fatalf("create pct=%d: %v", in.pct, err)
		}
	}

	alerts, err := svc.ListAlerts(ctx, wsA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(alerts) != 2 || alerts[0].ThresholdPct != 50 || alerts[1].ThresholdPct != 80 {
		t.Fatalf("alerts not listed ordered by threshold_pct asc: %+v", alerts)
	}

	replacement := "ops-" + uuid.NewString()[:8] + "@test.local"
	if _, err := svc.CreateAlert(ctx, budgets.CreateAlertInput{
		WorkspaceID: wsA, ThresholdPct: 80, Email: &replacement,
	}); err != nil {
		t.Fatalf("upsert duplicate: %v", err)
	}
	alerts, err = svc.ListAlerts(ctx, wsA)
	if err != nil {
		t.Fatalf("relist: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("duplicate pct produced a second alert, got %d", len(alerts))
	}
	for _, a := range alerts {
		if a.ThresholdPct == 80 && (a.Email == nil || *a.Email != replacement) {
			t.Fatalf("duplicate upsert did not overwrite email: %+v", a)
		}
	}

	target := alerts[1] // the 80% alert on wsA

	if _, err := svc.UpdateAlert(ctx, budgets.UpdateAlertInput{
		WorkspaceID: wsB, ID: target.ID, Email: &replacement,
	}); !errors.Is(err, budgets.ErrBudgetNotFound) {
		t.Fatalf("cross-workspace update returned %v, want ErrBudgetNotFound", err)
	}
	if err := svc.DeleteAlert(ctx, wsB, target.ID); !errors.Is(err, budgets.ErrBudgetNotFound) {
		t.Fatalf("cross-workspace delete returned %v, want ErrBudgetNotFound", err)
	}

	newEmail := "owner-" + uuid.NewString()[:8] + "@test.local"
	updated, err := svc.UpdateAlert(ctx, budgets.UpdateAlertInput{
		WorkspaceID: wsA, ID: target.ID, Email: &newEmail,
	})
	if err != nil {
		t.Fatalf("own-workspace update: %v", err)
	}
	if updated.Email == nil || *updated.Email != newEmail {
		t.Fatalf("update did not apply: %+v", updated)
	}
	if err := svc.DeleteAlert(ctx, wsA, target.ID); err != nil {
		t.Fatalf("own-workspace delete: %v", err)
	}
	alerts, err = svc.ListAlerts(ctx, wsA)
	if err != nil {
		t.Fatalf("post-delete list: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert after delete, got %d", len(alerts))
	}
}

// TestMonthToDateSpend_Live pins the aggregation query behind the #1127 index
// work: only usage_charge rows inside [periodStart, now] count; pre-period
// charges are excluded; a corrupted positive-delta usage_charge can drag the
// raw sum negative and MUST clamp to zero, never report negative spend.
func TestMonthToDateSpend_Live(t *testing.T) {
	pool := newBudgetsTestPool(t)
	wsID := seedBudgetWorkspace(t, pool)
	ctx := context.Background()
	repo := budgets.NewWorkspacePgxRepository(pool)

	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	seedCharge(t, pool, wsID, -300, time.Hour)
	seedCharge(t, pool, wsID, -200, 30*time.Minute)
	seedCharge(t, pool, wsID, -999, 40*24*time.Hour) // last month, excluded
	seedCharge(t, pool, wsID, 50, 15*time.Minute)    // corrupted positive delta

	spend, err := repo.MonthToDateSpendCredits(ctx, wsID, periodStart)
	if err != nil {
		t.Fatalf("mtd: %v", err)
	}
	if spend.Cmp(big.NewInt(450)) != 0 {
		t.Fatalf("spend=%s, want 450 (300+200-50)", spend.String())
	}

	// Boundary semantics: a charge stamped exactly at periodStart counts (>=).
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.credit_ledger_entries (account_id, entry_type, credits_delta, idempotency_key, request_id, created_at)
		 VALUES ($1, 'usage_charge', -25, $2, 'budgets-mtd-boundary', $3::timestamptz)`,
		wsID, "mtd-boundary-"+uuid.NewString(), periodStart.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("seed boundary charge: %v", err)
	}

	spend2, err := repo.MonthToDateSpendCredits(ctx, wsID, periodStart)
	if err != nil {
		t.Fatalf("mtd2: %v", err)
	}
	if spend2.Cmp(big.NewInt(475)) != 0 {
		t.Fatalf("spend=%s, want 475 including the boundary charge", spend2.String())
	}

	// Clamp guard alone: an account whose only usage_charge rows carry positive
	// deltas reports zero, never negative.
	wsOnlyPositive := seedBudgetWorkspace(t, pool)
	seedCharge(t, pool, wsOnlyPositive, 70, time.Hour)
	clamped, err := repo.MonthToDateSpendCredits(ctx, wsOnlyPositive, periodStart)
	if err != nil {
		t.Fatalf("clamped mtd: %v", err)
	}
	if clamped.Sign() < 0 {
		t.Fatalf("spend reported negative (%s); aggregation must never report negative spend", clamped.String())
	}
	if clamped.Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("positive-delta-only account spent %s, want 0", clamped.String())
	}
}

// TestCronEvaluatorBranches covers evaluateWorkspace guard branches the
// happy-path tests skip: zero soft cap disables percentage alerts, a failed
// dispatch leaves the alert unstamped so the next pass retries, repository
// errors surface, and one broken workspace does not block another's alerts.
func TestCronEvaluatorBranches(t *testing.T) {
	periodNow := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	t.Run("zero soft cap disables percentage alerts", func(t *testing.T) {
		repo := newFakeWorkspaceRepo()
		notifier := &fakeAlertNotifier{}
		ws := uuid.New()
		_, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
			WorkspaceID: ws, PeriodStart: periodNow,
			SoftCap: big.NewInt(0), HardCap: big.NewInt(10),
		})
		if err != nil {
			t.Fatalf("seed budget: %v", err)
		}
		if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
			WorkspaceID: ws, ThresholdPct: 50,
		}); err != nil {
			t.Fatalf("seed alert: %v", err)
		}

		cron := budgets.NewCronEvaluator(repo, notifier, nil)
		fired, err := cron.EvaluateBudgets(context.Background(), periodNow)
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if fired != 0 || notifier.calls != 0 {
			t.Fatalf("zero soft cap fired %d alerts (%d dispatches); percentage alerts must stay disabled",
				fired, notifier.calls)
		}
	})

	t.Run("failed dispatch leaves alert unstamped so the next pass retries", func(t *testing.T) {
		repo := newFakeWorkspaceRepo()
		notifier := &fakeAlertNotifier{failure: errors.New("webhook down")}
		ws := uuid.New()
		_, _ = repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
			WorkspaceID: ws, PeriodStart: periodNow,
			SoftCap: big.NewInt(1000), HardCap: big.NewInt(2000),
		})
		alert, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
			WorkspaceID: ws, ThresholdPct: 50,
		})
		if err != nil {
			t.Fatalf("seed alert: %v", err)
		}
		repo.mtd[ws] = spendCredits(500) // exactly at the 50% line

		cron := budgets.NewCronEvaluator(repo, notifier, nil)
		fired, err := cron.EvaluateBudgets(context.Background(), periodNow)
		if err != nil {
			t.Fatalf("evaluate with failing notifier: %v", err)
		}
		if fired != 0 {
			t.Fatalf("failed dispatch counted as fired=%d", fired)
		}
		stored, _ := repo.GetBudget(context.Background(), ws)
		_ = stored
		list, _ := repo.ListAlerts(context.Background(), ws)
		for _, a := range list {
			if a.ID == alert.ID && a.LastFiredPeriod != nil {
				t.Fatal("failed dispatch was stamped as fired; retry would be lost")
			}
		}

		// Notifier heals: the very next pass fires and stamps.
		notifier.mu.Lock()
		notifier.failure = nil
		notifier.mu.Unlock()
		fired, err = cron.EvaluateBudgets(context.Background(), periodNow.Add(time.Minute))
		if err != nil {
			t.Fatalf("evaluate after heal: %v", err)
		}
		if fired != 1 {
			t.Fatalf("healed pass fired=%d, want 1 (retry preserved)", fired)
		}
	})

	t.Run("repository errors surface to the caller", func(t *testing.T) {
		wantErr := errors.New("db unreachable")
		cron := budgets.NewCronEvaluator(&erroringWorkspaceRepo{err: wantErr}, &fakeAlertNotifier{}, nil)
		if _, err := cron.EvaluateBudgets(context.Background(), periodNow); !errors.Is(err, wantErr) {
			t.Fatalf("EvaluateBudgets returned %v, want wrapped %v", err, wantErr)
		}
	})

	t.Run("one broken workspace does not block another's alerts", func(t *testing.T) {
		repo := newFakeWorkspaceRepo()
		notifier := &fakeAlertNotifier{}

		healthy := uuid.New()
		if _, err := repo.UpsertBudget(context.Background(), budgets.SetBudgetInput{
			WorkspaceID: healthy, PeriodStart: periodNow,
			SoftCap: big.NewInt(1000), HardCap: big.NewInt(2000),
		}); err != nil {
			t.Fatalf("seed healthy budget: %v", err)
		}
		if _, err := repo.CreateAlert(context.Background(), budgets.CreateAlertInput{
			WorkspaceID: healthy, ThresholdPct: 50,
		}); err != nil {
			t.Fatalf("seed healthy alert: %v", err)
		}
		repo.mtd[healthy] = spendCredits(500)

		mixed := &mixedHealthRepo{healthy: repo, brokenIDs: map[uuid.UUID]bool{}}
		broken := uuid.New()
		mixed.brokenIDs[broken] = true
		mixed.all = append(mixed.all, broken, healthy)

		cron := budgets.NewCronEvaluator(mixed, notifier, nil)
		fired, err := cron.EvaluateBudgets(context.Background(), periodNow)
		if err != nil {
			t.Fatalf("evaluation must isolate per-workspace failures, got error: %v", err)
		}
		if fired != 1 {
			t.Fatalf("healthy workspace fired=%d, want 1", fired)
		}
		if notifier.calls != 1 {
			t.Fatalf("healthy alert dispatched %d times, want 1", notifier.calls)
		}
	})
}

// erroringWorkspaceRepo fails ListWorkspacesWithBudget immediately.
type erroringWorkspaceRepo struct{ err error }

func (e *erroringWorkspaceRepo) GetBudget(context.Context, uuid.UUID) (*budgets.Budget, error) {
	return nil, e.err
}
func (e *erroringWorkspaceRepo) UpsertBudget(context.Context, budgets.SetBudgetInput) (*budgets.Budget, error) {
	return nil, e.err
}
func (e *erroringWorkspaceRepo) DeleteBudget(context.Context, uuid.UUID) error { return e.err }
func (e *erroringWorkspaceRepo) ListAlerts(context.Context, uuid.UUID) ([]budgets.SpendAlert, error) {
	return nil, e.err
}
func (e *erroringWorkspaceRepo) CreateAlert(context.Context, budgets.CreateAlertInput) (*budgets.SpendAlert, error) {
	return nil, e.err
}
func (e *erroringWorkspaceRepo) UpdateAlert(context.Context, budgets.UpdateAlertInput) (*budgets.SpendAlert, error) {
	return nil, e.err
}
func (e *erroringWorkspaceRepo) DeleteAlert(context.Context, uuid.UUID, uuid.UUID) error {
	return e.err
}
func (e *erroringWorkspaceRepo) ListWorkspacesWithBudget(context.Context) ([]uuid.UUID, error) {
	return nil, e.err
}
func (e *erroringWorkspaceRepo) StampAlertFired(context.Context, uuid.UUID, time.Time, time.Time) error {
	return e.err
}
func (e *erroringWorkspaceRepo) MonthToDateSpendCredits(context.Context, uuid.UUID, time.Time) (*big.Int, error) {
	return nil, e.err
}

// mixedHealthRepo serves a fixed workspace list where some ids fail GetBudget.
type mixedHealthRepo struct {
	healthy   *fakeWorkspaceRepo
	brokenIDs map[uuid.UUID]bool
	all       []uuid.UUID
}

func (m *mixedHealthRepo) GetBudget(ctx context.Context, ws uuid.UUID) (*budgets.Budget, error) {
	if m.brokenIDs[ws] {
		return nil, errors.New("workspace row corrupted")
	}
	return m.healthy.GetBudget(ctx, ws)
}
func (m *mixedHealthRepo) UpsertBudget(ctx context.Context, in budgets.SetBudgetInput) (*budgets.Budget, error) {
	return m.healthy.UpsertBudget(ctx, in)
}
func (m *mixedHealthRepo) DeleteBudget(ctx context.Context, ws uuid.UUID) error {
	return m.healthy.DeleteBudget(ctx, ws)
}
func (m *mixedHealthRepo) ListAlerts(ctx context.Context, ws uuid.UUID) ([]budgets.SpendAlert, error) {
	return m.healthy.ListAlerts(ctx, ws)
}
func (m *mixedHealthRepo) CreateAlert(ctx context.Context, in budgets.CreateAlertInput) (*budgets.SpendAlert, error) {
	return m.healthy.CreateAlert(ctx, in)
}
func (m *mixedHealthRepo) UpdateAlert(ctx context.Context, in budgets.UpdateAlertInput) (*budgets.SpendAlert, error) {
	return m.healthy.UpdateAlert(ctx, in)
}
func (m *mixedHealthRepo) DeleteAlert(ctx context.Context, ws, id uuid.UUID) error {
	return m.healthy.DeleteAlert(ctx, ws, id)
}
func (m *mixedHealthRepo) ListWorkspacesWithBudget(context.Context) ([]uuid.UUID, error) {
	return m.all, nil
}
func (m *mixedHealthRepo) StampAlertFired(ctx context.Context, id uuid.UUID, firedAt, period time.Time) error {
	return m.healthy.StampAlertFired(ctx, id, firedAt, period)
}
func (m *mixedHealthRepo) MonthToDateSpendCredits(ctx context.Context, ws uuid.UUID, p time.Time) (*big.Int, error) {
	return m.healthy.MonthToDateSpendCredits(ctx, ws, p)
}

// TestCronSupportQueries_Live covers the cron-support queries against real
// rows: workspaces with a budget row are enumerated for evaluation, and a
// fired alert is stamped so alreadyFiredThisPeriod suppresses refires within
// the same period.
func TestCronSupportQueries_Live(t *testing.T) {
	pool := newBudgetsTestPool(t)
	wsID := seedBudgetWorkspace(t, pool)
	ctx := context.Background()
	repo := budgets.NewWorkspacePgxRepository(pool)

	if _, err := repo.UpsertBudget(ctx, budgets.SetBudgetInput{
		WorkspaceID: wsID,
		PeriodStart: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		SoftCap:     big.NewInt(1000),
		HardCap:     big.NewInt(2000),
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	alert, err := repo.CreateAlert(ctx, budgets.CreateAlertInput{
		WorkspaceID: wsID, ThresholdPct: 50,
	})
	if err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	workspaces, err := repo.ListWorkspacesWithBudget(ctx)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	found := false
	for _, id := range workspaces {
		if id == wsID {
			found = true
		}
	}
	if !found {
		t.Fatal("workspace with a budget row missing from the cron enumeration")
	}

	firedAt := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	if err := repo.StampAlertFired(ctx, alert.ID, firedAt, firedAt); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	alerts, err := repo.ListAlerts(ctx, wsID)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	for _, a := range alerts {
		if a.ID != alert.ID {
			continue
		}
		if a.LastFiredAt == nil || !a.LastFiredAt.Equal(firedAt) {
			t.Fatalf("last_fired_at=%v, want %v", a.LastFiredAt, firedAt)
		}
		// last_fired_period is a DATE column; it reads back as that day's
		// midnight UTC regardless of the stamp's clock time.
		wantPeriod := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		if a.LastFiredPeriod == nil || !a.LastFiredPeriod.Equal(wantPeriod) {
			t.Fatalf("last_fired_period=%v, want %v", a.LastFiredPeriod, wantPeriod)
		}
	}
}

// TestThresholdServiceValidation covers the legacy service wrappers:
// non-positive thresholds are refused before any write; DismissAlert and
// UpsertThreshold delegate to the repository.
func TestThresholdServiceValidation(t *testing.T) {
	repo := &mockRepo{}
	svc := budgets.NewService(repo, &mockNotifier{})

	if _, err := svc.UpsertThreshold(context.Background(), uuid.New(), budgets.UpsertThresholdInput{ThresholdCredits: 0}); err == nil {
		t.Fatal("zero threshold must be refused")
	}
	if _, err := svc.UpsertThreshold(context.Background(), uuid.New(), budgets.UpsertThresholdInput{ThresholdCredits: -5}); err == nil {
		t.Fatal("negative threshold must be refused")
	}

	acct := uuid.New()
	repo.threshold = &budgets.BudgetThreshold{
		ID: uuid.New(), AccountID: acct, ThresholdCredits: 1000,
	}
	upserted, err := svc.UpsertThreshold(context.Background(), acct, budgets.UpsertThresholdInput{ThresholdCredits: 1000})
	if err != nil {
		t.Fatalf("valid upsert: %v", err)
	}
	if upserted == nil {
		t.Fatal("valid upsert returned nil threshold")
	}

	var validationErr *budgets.ValidationError
	if !errors.As(svcValidationError(t, svc), &validationErr) {
		t.Fatal("zero-threshold refusal must be a *budgets.ValidationError")
	}
	if validationErr.Error() == "" {
		t.Fatal("validation error message must not be empty")
	}

	if err := svc.DismissAlert(context.Background(), acct); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
}

// svcValidationError re-runs the zero-threshold refusal and returns its error.
func svcValidationError(t *testing.T, svc *budgets.Service) error {
	t.Helper()
	_, err := svc.UpsertThreshold(context.Background(), uuid.New(), budgets.UpsertThresholdInput{ThresholdCredits: 0})
	return err
}

// TestServiceWorkspaceWrappers_Live covers the two service-level wrappers the
// edge gate and console read through, against real rows.
func TestServiceWorkspaceWrappers_Live(t *testing.T) {
	pool := newBudgetsTestPool(t)
	wsID := seedBudgetWorkspace(t, pool)
	ctx := context.Background()
	svc := newLiveWorkspaceService(pool)

	capValue, err := svc.HardCapForWorkspace(ctx, wsID)
	if err != nil {
		t.Fatalf("hard cap read: %v", err)
	}
	if capValue != nil {
		t.Fatalf("unbudgeted workspace returned %s, want nil (pass-through)", capValue.String())
	}

	spend, err := svc.MonthToDateSpendCredits(ctx, wsID, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("mtd spend: %v", err)
	}
	if spend.Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("empty account spent %s, want 0", spend.String())
	}
}

// TestSetBudgetRedisBroadcast_Live pins the push-on-write invalidation
// contract with the edge-api budget gate: SetBudget writes the hard cap under
// the gate's key and DeleteBudget removes it. Skips when no Redis is
// reachable so the CI leg (Postgres only) stays green.
func TestSetBudgetRedisBroadcast_Live(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "redis:6379"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unreachable from this environment: %v", err)
	}

	pool := newBudgetsTestPool(t)
	wsID := seedBudgetWorkspace(t, pool)

	svc := budgets.NewServiceWithWorkspace(
		&legacyNoopRepo{}, &legacyNoopNotifier{},
		budgets.NewWorkspacePgxRepository(pool), &fakeAlertNotifier{}, client,
	)

	if _, err := svc.SetBudget(ctx, budgets.SetBudgetInput{
		WorkspaceID: wsID,
		SoftCap:     big.NewInt(50_000),
		HardCap:     big.NewInt(100_000),
	}); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	key := "budget:hard_cap:{" + wsID.String() + "}"
	got, err := client.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("gate key missing after SetBudget: %v", err)
	}
	if got != "100000" {
		t.Fatalf("gate key carries %q, want \"100000\"", got)
	}

	if err := svc.DeleteBudget(ctx, wsID); err != nil {
		t.Fatalf("delete budget: %v", err)
	}
	if _, err := client.Get(ctx, key).Result(); !errors.Is(err, goredis.Nil) {
		t.Fatalf("gate key survived DeleteBudget: %v", err)
	}
}

// TestUnwiredWorkspaceSurfaceRefuses keeps the constructor contract honest:
// a Service built with the legacy-only constructor must refuse every
// workspace call instead of panicking on its nil context.
func TestUnwiredWorkspaceSurfaceRefuses(t *testing.T) {
	svc := budgets.NewService(&mockRepo{}, &mockNotifier{})
	ctx := context.Background()

	if _, err := svc.GetBudget(ctx, uuid.New()); err == nil {
		t.Fatal("unwired GetBudget must refuse")
	}
	if _, err := svc.SetBudget(ctx, budgets.SetBudgetInput{WorkspaceID: uuid.New()}); err == nil {
		t.Fatal("unwired SetBudget must refuse")
	}
	if err := svc.DeleteBudget(ctx, uuid.New()); err == nil {
		t.Fatal("unwired DeleteBudget must refuse")
	}
	if _, err := svc.ListAlerts(ctx, uuid.New()); err == nil {
		t.Fatal("unwired ListAlerts must refuse")
	}
	if _, err := svc.CreateAlert(ctx, budgets.CreateAlertInput{WorkspaceID: uuid.New(), ThresholdPct: 50}); err == nil {
		t.Fatal("unwired CreateAlert must refuse")
	}
	if _, err := svc.UpdateAlert(ctx, budgets.UpdateAlertInput{WorkspaceID: uuid.New(), ID: uuid.New()}); err == nil {
		t.Fatal("unwired UpdateAlert must refuse")
	}
	if err := svc.DeleteAlert(ctx, uuid.New(), uuid.New()); err == nil {
		t.Fatal("unwired DeleteAlert must refuse")
	}
	if _, err := svc.HardCapForWorkspace(ctx, uuid.New()); err == nil {
		t.Fatal("unwired HardCapForWorkspace must refuse")
	}
	if _, err := svc.MonthToDateSpendCredits(ctx, uuid.New(), time.Now()); err == nil {
		t.Fatal("unwired MonthToDateSpendCredits must refuse")
	}
}

// TestThresholdCrossedGuards completes the guard table for the big.Int cross
// math: nil operands and non-positive percentages answer false rather than
// panicking or dividing by zero.
func TestThresholdCrossedGuards(t *testing.T) {
	cases := []struct {
		name string
		mtd  *big.Int
		cap  *big.Int
		pct  int
		want bool
	}{
		{"nil mtd", nil, big.NewInt(100), 50, false},
		{"nil cap", big.NewInt(100), nil, 50, false},
		{"negative pct", big.NewInt(100), big.NewInt(100), -1, false},
		{"zero pct", big.NewInt(100), big.NewInt(100), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := budgets.ThresholdCrossed(tc.mtd, tc.cap, tc.pct); got != tc.want {
				t.Fatalf("ThresholdCrossed(%v, %v, %d) = %v, want %v", tc.mtd, tc.cap, tc.pct, got, tc.want)
			}
		})
	}
}

// TestCronSkipsBudgetlessWorkspace: a workspace id that reaches the evaluator
// but has no budget row contributes nothing and errors nothing.
func TestCronSkipsBudgetlessWorkspace(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	notifier := &fakeAlertNotifier{}

	// Listed for evaluation but carrying neither corruption nor a budget row.
	mixed := &mixedHealthRepo{healthy: repo, brokenIDs: map[uuid.UUID]bool{}}
	budgetless := uuid.New()
	mixed.all = []uuid.UUID{budgetless}

	cron := budgets.NewCronEvaluator(mixed, notifier, nil)
	fired, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	if err != nil || fired != 0 {
		t.Fatalf("budgetless workspace returned (%d, %v), want (0, nil)", fired, err)
	}
}

// TestCronStampFailureRetries pins the second fail-closed half of dispatch:
// a failed last_fired_period stamp must not count the alert as fired, so the
// next pass retries instead of losing the alert for the whole period.
func TestCronStampFailureRetries(t *testing.T) {
	base := newFakeWorkspaceRepo()
	stamper := &flakyStampRepo{fakeWorkspaceRepo: base}
	notifier := &fakeAlertNotifier{}
	ws := uuid.New()
	_, _ = stamper.UpsertBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: ws,
		PeriodStart: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		SoftCap:     big.NewInt(1000),
		HardCap:     big.NewInt(2000),
	})
	if _, err := stamper.CreateAlert(context.Background(), budgets.CreateAlertInput{
		WorkspaceID: ws, ThresholdPct: 50,
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	base.mtd[ws] = spendCredits(500)

	stamper.failStamps = true
	cron := budgets.NewCronEvaluator(stamper, notifier, nil)
	fired, err := cron.EvaluateBudgets(context.Background(), time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if fired != 0 {
		t.Fatalf("failed stamp counted as fired=%d; it must be retried next pass", fired)
	}

	// Heal the repository: the very next pass fires exactly once, proving the
	// alert was never marked sent during the failing window.
	stamper.failStamps = false
	fired, err = cron.EvaluateBudgets(context.Background(), time.Date(2026, 8, 5, 12, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("healed evaluate: %v", err)
	}
	if fired != 1 {
		t.Fatalf("healed pass fired=%d, want 1", fired)
	}
}

// flakyStampRepo fails StampAlertFired until released, to exercise the retry
// path that keeps an alert alive across passes when stamping breaks.
type flakyStampRepo struct {
	*fakeWorkspaceRepo
	failStamps bool
}

func (f *flakyStampRepo) StampAlertFired(ctx context.Context, id uuid.UUID, at, period time.Time) error {
	if f.failStamps {
		return errors.New("stamp write lost")
	}
	return f.fakeWorkspaceRepo.StampAlertFired(ctx, id, at, period)
}

// failingThresholdRepo fails every operation, to prove the legacy service
// wraps and surfaces repository failures instead of answering defaults.
type failingThresholdRepo struct {
	err error
}

func (f *failingThresholdRepo) GetThreshold(context.Context, uuid.UUID) (*budgets.BudgetThreshold, error) {
	return nil, f.err
}
func (f *failingThresholdRepo) UpsertThreshold(context.Context, uuid.UUID, int64) (*budgets.BudgetThreshold, error) {
	return nil, f.err
}
func (f *failingThresholdRepo) DismissAlert(context.Context, uuid.UUID) error { return f.err }
func (f *failingThresholdRepo) MarkNotified(context.Context, uuid.UUID) error { return f.err }

// TestLegacyServiceErrorPropagation keeps the fail-loud contract on the
// legacy threshold surface: repository failures surface from every wrapper,
// and CheckThresholds refuses to answer on an unreadable threshold.
func TestLegacyServiceErrorPropagation(t *testing.T) {
	repo := &failingThresholdRepo{err: errors.New("db down")}
	svc := budgets.NewService(repo, &mockNotifier{})
	ctx := context.Background()

	if _, err := svc.GetThreshold(ctx, uuid.New()); err == nil {
		t.Fatal("GetThreshold must surface repo failure")
	}
	if _, err := svc.UpsertThreshold(ctx, uuid.New(), budgets.UpsertThresholdInput{ThresholdCredits: 100}); err == nil {
		t.Fatal("UpsertThreshold must surface repo failure")
	}
	if err := svc.DismissAlert(ctx, uuid.New()); err == nil {
		t.Fatal("DismissAlert must surface repo failure")
	}
	if err := svc.CheckThresholds(ctx, uuid.New(), 100); err == nil {
		t.Fatal("CheckThresholds must fail closed when the threshold cannot be read")
	}
}
