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
	"github.com/jackc/pgx/v5/pgxpool"
)

func connectCatalogTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CATALOG_TEST_DB_URL")
	if dsn == "" {
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

	// Seed two aliases unique to this test run.
	suffix := fmt.Sprintf("integ-%d", time.Now().UnixNano())
	pubAlias := "pub-alias-" + suffix
	restAlias := "res-alias-" + suffix
	seedAlias(t, pool, pubAlias, "public")
	seedAlias(t, pool, restAlias, "restricted")

	// Cleanup visibility rows for both tenants on exit.
	t.Cleanup(func() {
		deleteVisibilityRow(t, pool, tenantA, pubAlias)
		deleteVisibilityRow(t, pool, tenantA, restAlias)
		deleteVisibilityRow(t, pool, tenantB, pubAlias)
		deleteVisibilityRow(t, pool, tenantB, restAlias)
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
