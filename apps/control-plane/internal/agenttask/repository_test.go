package agenttask_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// newRLSTestPool connects as the hive_app role — NOT BYPASSRLS in production
// (20260518_04_phase19_audit_rls_and_indexes.sql) — so the agent_tasks
// tenant-isolation RLS policy is actually exercised. Mirrors
// apps/control-plane/internal/marketplace/repository_test.go's helper of the
// same name.
func newRLSTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse HIVE_TEST_DB_URL: %v", err)
	}
	cfg.MaxConns = 1

	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(ctx, "SET ROLE hive_app"); err != nil {
		pool.Close()
		t.Skipf("SET ROLE hive_app failed (is hive_app provisioned + migrations applied on this test DB?): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant mirrors marketplace/repository_test.go's helper of the same
// name: a short-lived, unscoped connection inserts the FK row
// public.tenants requires, since hive_app has no INSERT policy on that table.
func seedTenant(t *testing.T, id uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()
	_, err = setup.Exec(ctx,
		`INSERT INTO public.tenants (id, slug, name, deployment)
		 VALUES ($1, $2, 'agenttask test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "agenttask-test-"+id.String())
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id)
	})
}

// seedUser inserts a minimal auth.users row so agent_tasks.user_id's FK is
// satisfiable. Mirrors apps/control-plane/internal/tenants/http_test.go's
// mustInsertUser helper.
func seedUser(t *testing.T) uuid.UUID {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	var id uuid.UUID
	email := "agenttask-test-" + uuid.NewString() + "@example.invalid"
	err = setup.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, id)
	})
	return id
}

func TestRepository_CreateGetTransition_RoundTrip(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := agenttask.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	created, err := repo.Create(ctx, tenantID, userID, agenttask.PackCoding)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != agenttask.StatusQueued {
		t.Errorf("expected new task to be queued, got %v", created.Status)
	}

	got, err := repo.Get(ctx, tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Pack != agenttask.PackCoding {
		t.Errorf("Get = %+v, want pack=coding-pack", got)
	}

	transitioned, err := repo.Transition(ctx, tenantID, userID, created.ID, agenttask.StatusRunning, "session-abc", "", "")
	if err != nil {
		t.Fatalf("Transition to running: %v", err)
	}
	if transitioned.Status != agenttask.StatusRunning {
		t.Errorf("expected StatusRunning, got %v", transitioned.Status)
	}
	if transitioned.StartedAt == nil {
		t.Error("expected started_at to be set on transition to running")
	}
	if transitioned.EngineSessionRef != "session-abc" {
		t.Errorf("expected engine_session_ref persisted, got %q", transitioned.EngineSessionRef)
	}

	finished, err := repo.Transition(ctx, tenantID, userID, created.ID, agenttask.StatusSucceeded, "", "result-ref", "")
	if err != nil {
		t.Fatalf("Transition to succeeded: %v", err)
	}
	if finished.FinishedAt == nil {
		t.Error("expected finished_at to be set on transition to succeeded")
	}
	if finished.ResultSummaryRef != "result-ref" {
		t.Errorf("expected result_summary_ref persisted, got %q", finished.ResultSummaryRef)
	}
}

// TestRepository_Transition_AtomicGuardRejectsAlreadyTerminal proves the
// UPDATE's own "not already terminal" precondition is what rejects a second
// transition, not an application-level pre-read — the fix for the
// last-write-wins race where a concurrent engine callback could otherwise
// clobber a Cancel (or vice versa). Two sequential Transitions: the first
// reaches a terminal state, the second must fail with ErrTerminalState
// rather than silently overwriting it.
func TestRepository_Transition_AtomicGuardRejectsAlreadyTerminal(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := agenttask.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	created, err := repo.Create(ctx, tenantID, userID, agenttask.PackCoding)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	cancelled, err := repo.Transition(ctx, tenantID, userID, created.ID, agenttask.StatusCancelled, "", "", "")
	if err != nil {
		t.Fatalf("first Transition (cancel): %v", err)
	}
	if cancelled.Status != agenttask.StatusCancelled {
		t.Fatalf("expected StatusCancelled, got %v", cancelled.Status)
	}

	if _, err := repo.Transition(ctx, tenantID, userID, created.ID, agenttask.StatusSucceeded, "", "result-ref", ""); !errors.Is(err, agenttask.ErrTerminalState) {
		t.Fatalf("expected ErrTerminalState on a second transition targeting an already-terminal row, got %v", err)
	}

	// The row must be unchanged: still cancelled, result_summary_ref never
	// applied by the rejected transition.
	got, err := repo.Get(ctx, tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Get after rejected transition: %v", err)
	}
	if got.Status != agenttask.StatusCancelled {
		t.Errorf("expected status to remain cancelled, got %v", got.Status)
	}
	if got.ResultSummaryRef != "" {
		t.Errorf("expected result_summary_ref to remain empty, got %q", got.ResultSummaryRef)
	}
}

// TestRepository_RLS_CrossTenantContextCannotReadRows proves the database
// policy itself blocks cross-tenant reads, independent of the repository's
// own tenant_id filter.
func TestRepository_RLS_CrossTenantContextCannotReadRows(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := agenttask.NewPgxRepository(pool)
	ctx := context.Background()

	tenantA, tenantB := uuid.New(), uuid.New()
	seedTenant(t, tenantA)
	seedTenant(t, tenantB)
	userID := seedUser(t)

	created, err := repo.Create(ctx, tenantA, userID, agenttask.PackCoding)
	if err != nil {
		t.Fatalf("seed tenant A task: %v", err)
	}

	if _, err := pool.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, false)", tenantB.String()); err != nil {
		t.Fatalf("set_config tenant B: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.agent_tasks WHERE id = $1`, created.ID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("RLS did not block cross-tenant read: got %d row(s) for tenant A's task while session claimed tenant B", count)
	}
}

// TestRepository_ScopedByUser_OtherUserCannotSeeOrCancel proves the
// application-layer user_id filter (not RLS) keeps a task private to the
// user who started it within the same tenant.
func TestRepository_ScopedByUser_OtherUserCannotSeeOrCancel(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := agenttask.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	owner := seedUser(t)
	other := seedUser(t)

	created, err := repo.Create(ctx, tenantID, owner, agenttask.PackKnowledgeWork)
	if err != nil {
		t.Fatalf("seed owner task: %v", err)
	}

	if _, err := repo.Get(ctx, tenantID, other, created.ID); err == nil {
		t.Fatal("expected Get to fail for a different user in the same tenant")
	}

	list, err := repo.List(ctx, tenantID, other)
	if err != nil {
		t.Fatalf("List(other): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected other user's task list to be empty, got %d", len(list))
	}

	if _, err := repo.Transition(ctx, tenantID, other, created.ID, agenttask.StatusCancelled, "", "", ""); err == nil {
		t.Fatal("expected Transition to fail for a different user in the same tenant")
	}
}

// TestRepository_RLS_NoSessionLeakAcrossBorrows proves the tenant context set
// by one repository call does not survive onto the pooled connection for
// whoever borrows it next.
func TestRepository_RLS_NoSessionLeakAcrossBorrows(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := agenttask.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	created, err := repo.Create(ctx, tenantID, userID, agenttask.PackCoding)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	var setting string
	if err := pool.QueryRow(ctx, "SELECT current_setting('app.current_tenant_id', true)").Scan(&setting); err != nil {
		t.Fatalf("read current_setting: %v", err)
	}
	if setting != "" {
		t.Fatalf("session leak: app.current_tenant_id still %q after Create committed, want empty", setting)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.agent_tasks WHERE id = $1`, created.ID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("session leak: bare borrow saw %d row(s) with no tenant context set (RLS should fail-closed on NULL)", count)
	}
}
