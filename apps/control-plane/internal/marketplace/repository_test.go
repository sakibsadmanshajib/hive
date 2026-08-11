package marketplace_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/marketplace"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/testdb"
)

// newRLSTestPool connects as the hive_app role — NOT BYPASSRLS in production
// (20260518_04_phase19_audit_rls_and_indexes.sql) — so the
// marketplace_tenant_entries tenant-isolation RLS policy is actually
// exercised. Mirrors apps/control-plane/internal/egress/repository_test.go's
// helper of the same name; see there for the full MaxConns=1 rationale.
func newRLSTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testdb.RequireTestDSN(t)

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

// newCatalogPool connects without SET ROLE, which is the posture the catalog
// half of this repository actually runs under.
//
// public.marketplace_entries is deliberately not granted to hive_app
// (20260716_01_marketplace_catalog.sql: "No RLS and no GRANT to authenticated:
// this is shared platform catalog data, not tenant data"), so driving entry
// CRUD through the hive_app pool asserts the opposite of the shipped trust
// posture and fails on "permission denied for table marketplace_entries". The
// tenant-enablement half, which is RLS-scoped, keeps using newRLSTestPool.
func newCatalogPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testdb.RequireTestDSN(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant mirrors egress/repository_test.go's helper of the same name: a
// short-lived, unscoped connection inserts the FK row public.tenants
// requires, since hive_app has no INSERT policy on that table.
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
		 VALUES ($1, $2, 'marketplace test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "marketplace-test-"+id.String())
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

func TestRepository_CatalogCRUD_RoundTrip(t *testing.T) {
	repo := marketplace.NewPgxRepository(newCatalogPool(t))
	ctx := context.Background()

	created, err := repo.CreateEntry(ctx, marketplace.Entry{
		Kind:        marketplace.KindMCPServer,
		Name:        "repo-test-github-" + uuid.NewString(),
		Description: "GitHub MCP server",
		Config:      json.RawMessage(`{"command":"npx","args":["-y","server-github"]}`),
	})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteEntry(context.Background(), created.ID) })

	got, err := repo.GetEntry(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if got.Name != created.Name || got.Kind != marketplace.KindMCPServer {
		t.Errorf("GetEntry = %+v, want name=%q kind=mcp_server", got, created.Name)
	}

	updated, err := repo.UpdateEntry(ctx, created.ID, created.Name, "updated description", json.RawMessage(`{"command":"npx","args":["-y","server-github","--flag"]}`))
	if err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if updated.Description != "updated description" {
		t.Errorf("UpdateEntry description = %q, want %q", updated.Description, "updated description")
	}

	if err := repo.DeleteEntry(ctx, created.ID); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if _, err := repo.GetEntry(ctx, created.ID); err == nil {
		t.Error("expected GetEntry to fail after DeleteEntry")
	}
}

// TestRepository_CatalogWriteDeniedToTenantRole pins the grant posture the
// catalog migration describes in prose: hive_app, the role every tenant-scoped
// query runs as, cannot write the global catalog. A migration that granted it
// by accident would otherwise show up only as a privilege escalation nobody
// tested for.
func TestRepository_CatalogWriteDeniedToTenantRole(t *testing.T) {
	repo := marketplace.NewPgxRepository(newRLSTestPool(t))
	ctx := context.Background()

	if _, err := repo.CreateEntry(ctx, marketplace.Entry{
		Kind:   marketplace.KindMCPServer,
		Name:   "repo-test-denied-" + uuid.NewString(),
		Config: json.RawMessage(`{"command":"npx"}`),
	}); err == nil {
		t.Fatal("hive_app wrote public.marketplace_entries; the catalog is platform data and must not be writable by the tenant role")
	}
}

func TestRepository_TenantEnablement_RLSIsolation(t *testing.T) {
	// The catalog row is created with the platform's own pool, the
	// enablement rows through the RLS-scoped one, which is how the two halves
	// are reached in production.
	catalog := marketplace.NewPgxRepository(newCatalogPool(t))
	repo := marketplace.NewPgxRepository(newRLSTestPool(t))
	ctx := context.Background()

	entry, err := catalog.CreateEntry(ctx, marketplace.Entry{
		Kind:   marketplace.KindMCPServer,
		Name:   "repo-test-rls-" + uuid.NewString(),
		Config: json.RawMessage(`{"command":"npx"}`),
	})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	t.Cleanup(func() { _ = catalog.DeleteEntry(context.Background(), entry.ID) })

	tenantA, tenantB := uuid.New(), uuid.New()
	seedTenant(t, tenantA)
	seedTenant(t, tenantB)
	// uuid.Nil writes enabled_by as NULL. A random UUID would be a
	// non-existent auth.users id and fail the enabled_by foreign key, which
	// this suite would then read as "the entry does not exist" (SetEnabled
	// maps any 23503 to ErrNotFound). Seeding a real auth user buys nothing
	// here: no assertion below looks at enabled_by.
	actor := uuid.Nil

	if err := repo.SetEnabled(ctx, tenantA, entry.ID, true, actor); err != nil {
		t.Fatalf("SetEnabled(tenantA): %v", err)
	}

	enabledA, err := repo.EnabledEntryIDs(ctx, tenantA)
	if err != nil {
		t.Fatalf("EnabledEntryIDs(tenantA): %v", err)
	}
	if _, ok := enabledA[entry.ID]; !ok {
		t.Error("expected entry enabled for tenantA")
	}

	// RLS isolation: tenantB never enabled this entry and must not see it,
	// even though both rows would otherwise be visible on an unscoped query.
	enabledB, err := repo.EnabledEntryIDs(ctx, tenantB)
	if err != nil {
		t.Fatalf("EnabledEntryIDs(tenantB): %v", err)
	}
	if _, ok := enabledB[entry.ID]; ok {
		t.Error("RLS isolation violated: tenantB saw tenantA's enablement")
	}

	if err := repo.SetEnabled(ctx, tenantA, entry.ID, false, actor); err != nil {
		t.Fatalf("SetEnabled(tenantA, disable): %v", err)
	}
	enabledA, err = repo.EnabledEntryIDs(ctx, tenantA)
	if err != nil {
		t.Fatalf("EnabledEntryIDs(tenantA) after disable: %v", err)
	}
	if _, ok := enabledA[entry.ID]; ok {
		t.Error("expected entry disabled for tenantA after SetEnabled(false)")
	}
}

func TestRepository_SetEnabled_UnknownEntry_ForeignKeyViolation(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := marketplace.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)

	// The actor is uuid.Nil so the only foreign key that can fail is the one
	// this test is named after, entry_id. A random actor would fail the
	// enabled_by key instead and pass the assertion for the wrong reason.
	err := repo.SetEnabled(ctx, tenantID, uuid.New(), true, uuid.Nil)
	if err == nil {
		t.Fatal("expected an error enabling a non-existent entry")
	}
}
