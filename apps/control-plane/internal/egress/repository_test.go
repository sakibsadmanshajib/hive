package egress_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/egress"
)

// newRLSTestPool connects as the hive_app role — NOT BYPASSRLS in production
// (see 20260518_04_phase19_audit_rls_and_indexes.sql) — so the
// egress_policies tenant-isolation RLS policy is actually exercised rather
// than bypassed by whatever superuser role HIVE_TEST_DB_URL otherwise
// connects as. MaxConns is pinned to 1 so every Acquire inside a test
// returns the same physical connection the SET ROLE was issued on; the pool
// is closed (not returned to a shared pool) at test end so the role change
// never leaks to another test.
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

// seedTenant inserts an FK row into public.tenants via a short-lived,
// unscoped connection (hive_app has no INSERT policy on tenants — only the
// authenticated-role self-read policy from 20260516_01_phase19_tenants.sql —
// so RLS setup for this table is not this package's concern to exercise).
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
		 VALUES ($1, $2, 'egress test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "egress-test-"+id.String())
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

func TestPgxRepository_RLS_PutThenGetRoundTripsUnderTenantContext(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantID := uuid.New()
	seedTenant(t, tenantID)

	repo := egress.NewPgxRepository(pool)
	ctx := context.Background()

	if _, err := repo.UpsertTenantDefault(ctx, tenantID, []string{"pypi.org", "github.com"}); err != nil {
		t.Fatalf("UpsertTenantDefault: %v", err)
	}

	got, err := repo.GetTenantDefault(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetTenantDefault: %v", err)
	}
	if len(got.AllowedHosts) != 2 {
		t.Fatalf("expected 2 hosts round-tripped, got %v", got.AllowedHosts)
	}
}

// TestPgxRepository_RLS_CrossTenantContextCannotReadRows proves the database
// policy itself blocks cross-tenant reads, independent of the application's
// own WHERE clause. It sets the session to tenant B's context directly (not
// via the Repository, which always keeps the session var and the WHERE
// clause in lockstep) and queries for tenant A's row by id — the row exists,
// but RLS must still hide it.
func TestPgxRepository_RLS_CrossTenantContextCannotReadRows(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA, tenantB := uuid.New(), uuid.New()
	seedTenant(t, tenantA)
	seedTenant(t, tenantB)

	repo := egress.NewPgxRepository(pool)
	ctx := context.Background()
	if _, err := repo.UpsertTenantDefault(ctx, tenantA, []string{"tenant-a-only.example"}); err != nil {
		t.Fatalf("seed tenant A row: %v", err)
	}

	if _, err := pool.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, false)", tenantB.String()); err != nil {
		t.Fatalf("set_config tenant B: %v", err)
	}
	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.egress_policies WHERE tenant_id = $1 AND user_id IS NULL`,
		tenantA).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("RLS did not block cross-tenant read: got %d row(s) for tenant A while session claimed tenant B", count)
	}
}
