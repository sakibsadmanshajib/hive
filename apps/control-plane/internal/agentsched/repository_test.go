package agentsched_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agentsched"
)

// newRLSTestPool connects as hive_app so the agent_task_schedules
// tenant-isolation RLS policy is actually exercised. Mirrors
// apps/control-plane/internal/agenttask/repository_test.go's helper.
func newRLSTestPool(t *testing.T) *pgxpool.Pool {
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
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database")
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
		// Same loudness contract as the DSN gate: role provisioning failure in
		// CI is a schema regression, never a normal day.
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatalf("SET ROLE hive_app failed: %v", err)
		}
		t.Skipf("SET ROLE hive_app failed: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant inserts the FK row public.tenants requires.
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
		 VALUES ($1, $2, 'agentsched test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "agentsched-test-"+id.String())
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err == nil {
			defer cleanup.Close()
			_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id)
		}
	})
}

// seedUser inserts a minimal auth.users row for user_id FK.
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
	email := "agentsched-test-" + uuid.NewString() + "@example.invalid"
	err = setup.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err == nil {
			defer cleanup.Close()
			_, _ = cleanup.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, id)
		}
	})
	return id
}

// mustCreate inserts a schedule through the repository and fails the test on
// error.
// adminExec runs one statement as the migration owner (postgres), bypassing
// RLS, for test-only state overrides the hive_app pool cannot perform (the
// tenant GUC is not set on it, so RLS denies every direct UPDATE).
func adminExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer setup.Close()
	if _, err := setup.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("adminExec %s: %v", sql, err)
	}
}

func mustCreate(t *testing.T, repo agentsched.Repository, tenantID, userID uuid.UUID, name string) agentsched.Schedule {
	t.Helper()
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	s, err := repo.Create(context.Background(), agentsched.Schedule{
		TenantID:     tenantID,
		UserID:       userID,
		Name:         name,
		Instructions: "test instructions for " + name,
		Schedule:     "daily",
		Enabled:      true,
		NextRunAt:    &now,
	})
	if err != nil {
		t.Fatalf("seed schedule %q: %v", name, err)
	}
	return s
}

func TestRepository_CRUDRoundTripAndScoping(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := agentsched.NewPgxRepository(pool)
	ctx := context.Background()

	tenantA := uuid.New()
	seedTenant(t, tenantA)
	userA := seedUser(t)

	created := mustCreate(t, repo, tenantA, userA, "round-trip")
	if created.ID == uuid.Nil {
		t.Fatal("expected a persisted id")
	}

	got, err := repo.Get(ctx, tenantA, userA, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "round-trip" || got.Schedule != "daily" || !got.Enabled {
		t.Fatalf("Get = %+v", got)
	}

	// Cross-user read inside the same tenant reads as not found.
	userB := seedUser(t)
	if _, err := repo.Get(ctx, tenantA, userB, created.ID); !errors.Is(err, agentsched.ErrNotFound) {
		t.Fatalf("cross-user Get = %v, want ErrNotFound (404)", err)
	}

	// Cross-tenant read reads as not found too (RLS hides the row).
	tenantB := uuid.New()
	seedTenant(t, tenantB)
	if _, err := repo.Get(ctx, tenantB, userA, created.ID); !errors.Is(err, agentsched.ErrNotFound) {
		t.Fatalf("cross-tenant Get = %v, want ErrNotFound (404)", err)
	}

	// Update scoped the same way.
	got.Name = "updated"
	if _, err := repo.Update(ctx, agentsched.Schedule{
		ID: created.ID, TenantID: tenantA, UserID: userA,
		Name: "updated", Instructions: got.Instructions,
		Schedule: got.Schedule, Enabled: true, NextRunAt: got.NextRunAt,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// SetEnabled round trip against the live RLS policy.
	reenabled := mustCreate(t, repo, tenantA, userA, "toggle")
	if _, err := repo.SetEnabled(ctx, tenantA, userA, reenabled.ID, false, nil); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	got2, _ := repo.Get(ctx, tenantA, userA, reenabled.ID)
	if got2.Enabled {
		t.Fatal("expected disabled after SetEnabled(false)")
	}
	next := got2.NextRunAt
	fresh := time.Now().UTC().Add(time.Hour)
	if _, err := repo.SetEnabled(ctx, tenantA, userA, reenabled.ID, true, &fresh); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	got2, _ = repo.Get(ctx, tenantA, userA, reenabled.ID)
	if !got2.Enabled {
		t.Fatal("expected enabled after SetEnabled(true)")
	}
	if next != nil && got2.NextRunAt != nil && got2.NextRunAt.Equal(*next) {
		t.Fatal("enable with a fresh next_run_at must overwrite the stored one")
	}

	// Delete by the wrong user is not found; by the owner removes the row.
	if err := repo.Delete(ctx, tenantA, userB, created.ID); !errors.Is(err, agentsched.ErrNotFound) {
		t.Fatalf("cross-user Delete = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, tenantA, userA, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(ctx, tenantA, userA, created.ID); !errors.Is(err, agentsched.ErrNotFound) {
		t.Fatal("second delete should be not found")
	}
}

// TestRepository_ClaimDueAgainstRealDB exercises the SECURITY DEFINER claim
// function through hive_app: only due enabled rows come back, each claim
// advances next_run_at by its cadence, a disabled row and a not-yet-due row
// stay untouched, and a second claim at the same instant returns nothing.
func TestRepository_ClaimDueAgainstRealDB(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := agentsched.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)

	due := mustCreate(t, repo, tenantID, userID, "due")
	notDue := mustCreate(t, repo, tenantID, userID, "not-due")
	adminExec(t,
		`UPDATE public.agent_task_schedules SET next_run_at = $1 WHERE id = $2`, past, due.ID)
	adminExec(t,
		`UPDATE public.agent_task_schedules SET next_run_at = $1 WHERE id = $2`, future, notDue.ID)
	disabled := mustCreate(t, repo, tenantID, userID, "disabled")
	adminExec(t,
		`UPDATE public.agent_task_schedules SET enabled = false, next_run_at = $1 WHERE id = $2`, past, disabled.ID)

	claimed, err := repo.ClaimDue(ctx, now, 100)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != due.ID {
		t.Fatalf("claimed %+v, want exactly the one due row", claimed)
	}
	if claimed[0].NextRunAt == nil || !claimed[0].NextRunAt.After(now) {
		t.Fatal("claim must advance next_run_at one cadence out")
	}

	again, err := repo.ClaimDue(ctx, now, 100)
	if err != nil {
		t.Fatalf("second ClaimDue: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim returned %d rows, want 0 (idempotent per tick)", len(again))
	}
}
