//go:build integration

package catalog

// Integration tests for tenant model visibility filtering.
//
// Prerequisites:
//   - A real Postgres database with Phase 20 migration applied.
//   - CATALOG_TEST_DB_URL environment variable.
//
// Run with:
//
//	go test -tags integration ./apps/control-plane/internal/catalog/...

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func connectCatalogTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CATALOG_TEST_DB_URL")
	if dsn == "" {
		// CI wires CATALOG_TEST_DB_URL at the job level (issue #708); a
		// missing value there means this suite silently skipped rather than
		// ran, the same failure shape #701/#705 fixed for litellmconfig.
		// Fail loud in CI instead of shipping an invisible skip; local dev
		// runs (CI unset) still skip.
		if os.Getenv("CI") != "" {
			t.Fatal("CATALOG_TEST_DB_URL not set in CI; this suite must not silently skip (issue #708)")
		}
		t.Skip("CATALOG_TEST_DB_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connectCatalogTestDB: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("connectCatalogTestDB ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant inserts a test public.tenants row so that tenant_id foreign
// keys on tenant_model_visibility resolve. tenant_model_visibility.tenant_id
// references tenants(id); the test's fixed tenantA/tenantB UUIDs must exist
// as real rows before any visibility row referencing them can be inserted.
// Returns true only if this call created the row (RETURNING with an
// ON CONFLICT DO NOTHING reports no row when one already existed), so the
// caller's cleanup deletes only tenant rows it actually owns.
func seedTenant(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID) bool {
	t.Helper()
	var inserted uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO public.tenants (id, slug, name, deployment)
		VALUES ($1, $2, $2, 'HIVE_CLOUD')
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`, tenantID, "test-tenant-"+tenantID.String()).Scan(&inserted)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false
		}
		t.Fatalf("seedTenant %v: %v", tenantID, err)
	}
	return true
}

// seedAlias inserts a test model_alias row. It is cleaned up via t.Cleanup.
func seedAlias(t *testing.T, pool *pgxpool.Pool, aliasID, visibility string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO public.model_aliases
			(alias_id, owned_by, display_name, summary, visibility, lifecycle,
			 capability_badges, input_price_credits, output_price_credits,
			 created_at, updated_at)
		VALUES ($1, 'test', $1, 'test alias', $2, 'stable',
			'[]'::jsonb, 10, 30, now(), now())
		ON CONFLICT (alias_id) DO NOTHING
	`, aliasID, visibility)
	if err != nil {
		t.Fatalf("seedAlias %q: %v", aliasID, err)
	}
	t.Cleanup(func() {
		// Remove visibility rows first (FK), then alias.
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM public.tenant_model_visibility WHERE alias_id = $1", aliasID)
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM public.model_aliases WHERE alias_id = $1", aliasID)
	})
}

// upsertVisibility inserts or updates a tenant_model_visibility row.
func upsertVisibilityRow(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, aliasID string, visible bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO public.tenant_model_visibility (tenant_id, alias_id, visible, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant_id, alias_id) DO UPDATE
			SET visible = EXCLUDED.visible, updated_at = now()
	`, tenantID, aliasID, visible)
	if err != nil {
		t.Fatalf("upsertVisibilityRow (%v, %s, %v): %v", tenantID, aliasID, visible, err)
	}
}

// deleteVisibilityRow removes a tenant_model_visibility row.
func deleteVisibilityRow(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, aliasID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		"DELETE FROM public.tenant_model_visibility WHERE tenant_id = $1 AND alias_id = $2",
		tenantID, aliasID)
	if err != nil {
		t.Fatalf("deleteVisibilityRow: %v", err)
	}
}

// aliasPresent reports whether aliasID appears in the list.
func aliasPresent(list []ModelAlias, aliasID string) bool {
	for _, a := range list {
		if a.AliasID == aliasID {
			return true
		}
	}
	return false
}

// TestTenantVisibilityIntegration runs an 8-step end-to-end visibility flow
// against a real Postgres database.
func TestTenantVisibilityIntegration(t *testing.T) {
	pool := connectCatalogTestDB(t)
	repo := NewPgxRepository(pool)
	svc := NewService(repo)
	ctx := context.Background()

	// Use UUIDs that are very unlikely to collide with real tenant data.
	tenantA := uuid.MustParse("a0000000-0000-0000-0000-000000000001")
	tenantB := uuid.MustParse("b0000000-0000-0000-0000-000000000001")
	ownsTenantA := seedTenant(t, pool, tenantA)
	ownsTenantB := seedTenant(t, pool, tenantB)

	// Seed two aliases unique to this test run.
	suffix := fmt.Sprintf("integ-%d", time.Now().UnixNano())
	pubAlias := "pub-alias-" + suffix
	restAlias := "res-alias-" + suffix
	seedAlias(t, pool, pubAlias, "public")
	seedAlias(t, pool, restAlias, "restricted")

	// Cleanup visibility rows for both tenants on exit, then the tenant rows
	// themselves, but only the ones this test actually created: a tenant row
	// already present before this run (ownsTenant* false) is not this test's
	// to delete, and might still be in use elsewhere.
	t.Cleanup(func() {
		deleteVisibilityRow(t, pool, tenantA, pubAlias)
		deleteVisibilityRow(t, pool, tenantA, restAlias)
		deleteVisibilityRow(t, pool, tenantB, pubAlias)
		deleteVisibilityRow(t, pool, tenantB, restAlias)
		if ownsTenantA {
			if _, err := pool.Exec(context.Background(), "DELETE FROM public.tenants WHERE id = $1", tenantA); err != nil {
				t.Errorf("cleanup: delete tenant A: %v", err)
			}
		}
		if ownsTenantB {
			if _, err := pool.Exec(context.Background(), "DELETE FROM public.tenants WHERE id = $1", tenantB); err != nil {
				t.Errorf("cleanup: delete tenant B: %v", err)
			}
		}
	})

	// -------------------------------------------------------------------------
	// Step 2: GET as tenant A (no visibility rows): public present, restricted absent.
	// -------------------------------------------------------------------------
	list, err := svc.ListModelsForTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("step 2: %v", err)
	}
	if !aliasPresent(list, pubAlias) {
		t.Errorf("step 2: public alias %q must be present for tenant with no override rows", pubAlias)
	}
	if aliasPresent(list, restAlias) {
		t.Errorf("step 2: restricted alias %q must be absent for tenant with no override rows", restAlias)
	}

	// -------------------------------------------------------------------------
	// Step 3: Insert visible=true for restricted alias; repeat GET.
	// -------------------------------------------------------------------------
	upsertVisibilityRow(t, pool, tenantA, restAlias, true)

	list, err = svc.ListModelsForTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("step 3: %v", err)
	}
	if !aliasPresent(list, restAlias) {
		t.Errorf("step 3: restricted alias %q must now be present after visible=true grant", restAlias)
	}

	// -------------------------------------------------------------------------
	// Step 5: Insert visible=false for public alias; repeat GET.
	// -------------------------------------------------------------------------
	upsertVisibilityRow(t, pool, tenantA, pubAlias, false)

	list, err = svc.ListModelsForTenant(ctx, tenantA)
	if err != nil {
		t.Fatalf("step 5: %v", err)
	}
	if aliasPresent(list, pubAlias) {
		t.Errorf("step 5: public alias %q must be absent after visible=false block", pubAlias)
	}
	// Restricted is still granted.
	if !aliasPresent(list, restAlias) {
		t.Errorf("step 5: restricted alias %q must still be present (grant not revoked)", restAlias)
	}

	// -------------------------------------------------------------------------
	// Step 7: GET as tenant B (no rows): still sees original public set.
	// -------------------------------------------------------------------------
	listB, err := svc.ListModelsForTenant(ctx, tenantB)
	if err != nil {
		t.Fatalf("step 7: %v", err)
	}
	if !aliasPresent(listB, pubAlias) {
		t.Errorf("step 7: tenant B must still see public alias %q (tenant A overrides must not bleed)", pubAlias)
	}
	if aliasPresent(listB, restAlias) {
		t.Errorf("step 7: tenant B must not see restricted alias %q (no grant exists)", restAlias)
	}

	t.Logf("TestTenantVisibilityIntegration: all steps passed (tenantA=%v, tenantB=%v)", tenantA, tenantB)
}

// TestFreePoolAliasesAreRestrictedFromTenants extends the mechanism proven
// above (TestTenantVisibilityIntegration) to the two real catalog rows this
// migration touches, by name, rather than duplicating the eight-step flow: it
// runs against whatever visibility class hive-free and hive-free-tools
// actually carry in public.model_aliases today, so it is RED against a
// database that predates
// 20260831_01_restrict_free_pool_aliases_visibility.sql (both rows are still
// 'public', so a fresh tenant with no grant row is entitled, and the
// assertions below fail) and GREEN once that migration has run.
//
// Both the listing path (svc.ListModelsForTenant, what the picker reads) and
// the invocation path (svc.IsAliasVisibleToTenant, the exact call
// routing.Service.SelectRoute makes before dispatching a completion) are
// checked, because AliasVisibleToTenant is the one predicate both resolve
// through and a regression could in principle diverge only one of them.
func TestFreePoolAliasesAreRestrictedFromTenants(t *testing.T) {
	pool := connectCatalogTestDB(t)
	repo := NewPgxRepository(pool)
	svc := NewService(repo)
	ctx := context.Background()

	tenant := uuid.MustParse("c0000000-0000-0000-0000-000000000001")
	ownsTenant := seedTenant(t, pool, tenant)
	t.Cleanup(func() {
		deleteVisibilityRow(t, pool, tenant, "hive-free")
		deleteVisibilityRow(t, pool, tenant, "hive-free-tools")
		if ownsTenant {
			if _, err := pool.Exec(context.Background(), "DELETE FROM public.tenants WHERE id = $1", tenant); err != nil {
				t.Errorf("cleanup: delete tenant: %v", err)
			}
		}
	})

	for _, aliasID := range []string{"hive-free", "hive-free-tools"} {
		var visibility string
		err := pool.QueryRow(ctx,
			"SELECT visibility FROM public.model_aliases WHERE alias_id = $1", aliasID,
		).Scan(&visibility)
		if err != nil {
			t.Fatalf("%s: not seeded in this database (expected from its own migration): %v", aliasID, err)
		}
		if visibility != "restricted" {
			t.Errorf("%s: visibility = %q, want %q (20260831_01_restrict_free_pool_aliases_visibility.sql not applied?)", aliasID, visibility, "restricted")
		}

		entitled, err := svc.IsAliasVisibleToTenant(ctx, tenant, aliasID)
		if err != nil {
			t.Fatalf("%s: IsAliasVisibleToTenant: %v", aliasID, err)
		}
		if entitled {
			t.Errorf("%s: IsAliasVisibleToTenant = true for a tenant with no visibility grant, want false (this is the exact call routing.Service.SelectRoute makes before dispatch)", aliasID)
		}
	}

	list, err := svc.ListModelsForTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("ListModelsForTenant: %v", err)
	}
	if aliasPresent(list, "hive-free") {
		t.Errorf("hive-free must be absent from the picker listing for a tenant with no grant")
	}
	if aliasPresent(list, "hive-free-tools") {
		t.Errorf("hive-free-tools must be absent from the picker listing for a tenant with no grant")
	}

	t.Logf("TestFreePoolAliasesAreRestrictedFromTenants: hive-free and hive-free-tools both locked for tenant=%v with no grant", tenant)
}
