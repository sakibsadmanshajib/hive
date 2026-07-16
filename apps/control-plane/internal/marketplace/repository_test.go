package marketplace_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/marketplace"
)

// newRLSTestPool connects as the hive_app role — NOT BYPASSRLS in production
// (20260518_04_phase19_audit_rls_and_indexes.sql) — so the
// marketplace_tenant_entries tenant-isolation RLS policy is actually
// exercised. Mirrors apps/control-plane/internal/egress/repository_test.go's
// helper of the same name; see there for the full MaxConns=1 rationale.
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
	pool := newRLSTestPool(t)
	repo := marketplace.NewPgxRepository(pool)
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

func TestRepository_TenantEnablement_RLSIsolation(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := marketplace.NewPgxRepository(pool)
	ctx := context.Background()

	entry, err := repo.CreateEntry(ctx, marketplace.Entry{
		Kind:   marketplace.KindMCPServer,
		Name:   "repo-test-rls-" + uuid.NewString(),
		Config: json.RawMessage(`{"command":"npx"}`),
	})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	t.Cleanup(func() { _ = repo.DeleteEntry(context.Background(), entry.ID) })

	tenantA, tenantB := uuid.New(), uuid.New()
	seedTenant(t, tenantA)
	seedTenant(t, tenantB)
	actor := uuid.New()

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

	err := repo.SetEnabled(ctx, tenantID, uuid.New(), true, uuid.New())
	if err == nil {
		t.Fatal("expected an error enabling a non-existent entry")
	}
}
