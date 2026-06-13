//go:build integration

package providers

// Integration tests for the providers package.
//
// Prerequisites:
//   - A real Postgres database with Phase 20 migration applied.
//   - PROVIDERS_TEST_DB_URL environment variable pointing to the test database.
//
// Run with:
//
//	go test -tags integration ./apps/control-plane/internal/providers/...
//
// These tests perform real DB operations and clean up after themselves.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	platformhttp "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/http"
)

// connectTestDB opens a pgxpool using PROVIDERS_TEST_DB_URL.
// Skips the test if the env var is not set.
func connectTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PROVIDERS_TEST_DB_URL")
	if dsn == "" {
		t.Skip("PROVIDERS_TEST_DB_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connectTestDB: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("connectTestDB ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// randomSuffix returns a short unique string derived from the current nanosecond
// timestamp for slug creation. Uniqueness relies on wall-clock resolution; callers
// in tight loops should append an additional counter if needed.
func randomSuffix() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

const integrationToken = "test-integration-secret"

// newIntegrationHandler builds the full providers HTTP handler wired to a real DB pool.
// It reuses the same package-level helpers (NewPgxRepository, NewService, NewHandler)
// from the production code, which are accessible because this file is in the same package.
func newIntegrationHandler(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	repo := NewPgxRepository(pool)
	svc := NewService(repo)
	h := NewHandler(svc)
	return platformhttp.RequireInternalToken(integrationToken, h.InternalMux())
}

// TestProviderCRUDIntegration exercises the full 7-step provider CRUD flow
// against a real Postgres database.
func TestProviderCRUDIntegration(t *testing.T) {
	pool := connectTestDB(t)
	handler := newIntegrationHandler(t, pool)
	ctx := context.Background()
	slug := "test-provider-" + randomSuffix()

	// -------------------------------------------------------------------------
	// Step 1: POST creates provider, returns 201 with valid JSON.
	// -------------------------------------------------------------------------
	createBody := map[string]any{
		"slug":           slug,
		"display_name":   "Integration Test Provider",
		"base_url":       "https://api.example.com/v1",
		"api_key_env":    "TEST_PROVIDER_API_KEY",
		"litellm_prefix": "test/",
		"enabled":        true,
	}
	req1 := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, createBody))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set(platformhttp.InternalTokenHeader, integrationToken)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("step 1: expected 201, got %d: %s", rr1.Code, rr1.Body.String())
	}
	created := decodeProvider(t, rr1.Body)
	if created.ID.String() == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("step 1: expected non-nil ID")
	}
	if created.Slug != slug {
		t.Fatalf("step 1: expected slug %q, got %q", slug, created.Slug)
	}
	providerID := created.ID

	// Cleanup: ensure the test row is removed even if the test fails mid-way.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.custom_providers WHERE id = $1", providerID)
	})

	// -------------------------------------------------------------------------
	// Step 2: GET /internal/providers/{id} returns the created row.
	// -------------------------------------------------------------------------
	req2 := httptest.NewRequest(http.MethodGet, "/internal/providers/"+providerID.String(), nil)
	req2.Header.Set(platformhttp.InternalTokenHeader, integrationToken)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("step 2: expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
	fetched := decodeProvider(t, rr2.Body)
	if fetched.ID != providerID {
		t.Fatalf("step 2: expected ID %v, got %v", providerID, fetched.ID)
	}
	if fetched.Slug != slug {
		t.Fatalf("step 2: expected slug %q, got %q", slug, fetched.Slug)
	}

	// -------------------------------------------------------------------------
	// Step 3: PUT updates display_name; verify updated value in response and DB.
	// -------------------------------------------------------------------------
	updateBody := map[string]any{
		"slug":           slug,
		"display_name":   "Integration Test Provider (updated)",
		"base_url":       "https://api.example.com/v1",
		"api_key_env":    "TEST_PROVIDER_API_KEY",
		"litellm_prefix": "test/",
		"enabled":        true,
	}
	req3 := httptest.NewRequest(http.MethodPut, "/internal/providers/"+providerID.String(), bodyJSON(t, updateBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set(platformhttp.InternalTokenHeader, integrationToken)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("step 3: expected 200, got %d: %s", rr3.Code, rr3.Body.String())
	}
	updated := decodeProvider(t, rr3.Body)
	if updated.DisplayName != "Integration Test Provider (updated)" {
		t.Fatalf("step 3: expected updated display_name, got %q", updated.DisplayName)
	}

	// Confirm the DB also reflects the change.
	var dbDisplayName string
	err := pool.QueryRow(ctx,
		"SELECT display_name FROM public.custom_providers WHERE id = $1", providerID,
	).Scan(&dbDisplayName)
	if err != nil {
		t.Fatalf("step 3: DB verify: %v", err)
	}
	if dbDisplayName != "Integration Test Provider (updated)" {
		t.Fatalf("step 3: DB display_name = %q, want updated value", dbDisplayName)
	}

	// -------------------------------------------------------------------------
	// Step 4: DELETE sets enabled=false; GET returns enabled: false.
	// -------------------------------------------------------------------------
	req4 := httptest.NewRequest(http.MethodDelete, "/internal/providers/"+providerID.String(), nil)
	req4.Header.Set(platformhttp.InternalTokenHeader, integrationToken)
	rr4 := httptest.NewRecorder()
	handler.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Fatalf("step 4: expected 200 on delete, got %d: %s", rr4.Code, rr4.Body.String())
	}
	deleted := decodeProvider(t, rr4.Body)
	if deleted.Enabled {
		t.Fatal("step 4: expected enabled=false after soft delete")
	}

	req4b := httptest.NewRequest(http.MethodGet, "/internal/providers/"+providerID.String(), nil)
	req4b.Header.Set(platformhttp.InternalTokenHeader, integrationToken)
	rr4b := httptest.NewRecorder()
	handler.ServeHTTP(rr4b, req4b)
	if rr4b.Code != http.StatusOK {
		t.Fatalf("step 4: GET after delete: expected 200, got %d", rr4b.Code)
	}
	afterDelete := decodeProvider(t, rr4b.Body)
	if afterDelete.Enabled {
		t.Fatal("step 4: GET after delete: expected enabled=false")
	}

	// -------------------------------------------------------------------------
	// Step 5: POST with same slug returns 409.
	// -------------------------------------------------------------------------
	req5 := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, createBody))
	req5.Header.Set("Content-Type", "application/json")
	req5.Header.Set(platformhttp.InternalTokenHeader, integrationToken)
	rr5 := httptest.NewRecorder()
	handler.ServeHTTP(rr5, req5)
	if rr5.Code != http.StatusConflict {
		t.Fatalf("step 5: expected 409 for duplicate slug, got %d: %s", rr5.Code, rr5.Body.String())
	}

	// -------------------------------------------------------------------------
	// Step 6: INSERT INTO provider_routes referencing the slug succeeds.
	// This verifies the CHECK constraint no longer enumerates fixed provider names.
	// Seed model_aliases first to satisfy the FK constraint on provider_routes.alias_id.
	// -------------------------------------------------------------------------
	const testAliasID = "test-integ-alias"
	_, err = pool.Exec(ctx, `
		INSERT INTO public.model_aliases
			(alias_id, owned_by, display_name, summary, visibility, lifecycle,
			 capability_badges, input_price_credits, output_price_credits, created_at, updated_at)
		VALUES ($1, 'test', $1, 'test', 'public', 'stable', '[]'::jsonb, 10, 30, now(), now())
		ON CONFLICT (alias_id) DO NOTHING
	`, testAliasID)
	if err != nil {
		t.Fatalf("step 6: seed model_aliases: %v", err)
	}
	// Cleanup alias row after route row (FK order: route first, then alias).
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.model_aliases WHERE alias_id = $1", testAliasID)
	})

	var routeID string
	err = pool.QueryRow(ctx, `
		INSERT INTO public.provider_routes
			(alias_id, provider, provider_model, litellm_model_name, price_class, health_state, priority)
		VALUES ($2, $1, 'test-model', 'test/test-model', 'standard', 'healthy', 1)
		RETURNING route_id
	`, slug, testAliasID).Scan(&routeID)
	if err != nil {
		t.Fatalf("step 6: INSERT provider_routes with custom slug: %v", err)
	}
	// Cleanup route row before alias row (FK order).
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.provider_routes WHERE route_id = $1", routeID)
	})

	t.Logf("TestProviderCRUDIntegration: all steps passed (provider_id=%s, route_id=%s)", providerID, routeID)
}

// TestProviderCRUDIntegration_MissingToken confirms 401 without auth header.
func TestProviderCRUDIntegration_MissingToken(t *testing.T) {
	pool := connectTestDB(t)
	handler := newIntegrationHandler(t, pool)
	req := httptest.NewRequest(http.MethodGet, "/internal/providers", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rr.Code)
	}
}
