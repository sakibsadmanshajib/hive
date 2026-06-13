//go:build integration

package litellmconfig_test

// Integration tests for SyncService.
//
// Prerequisites:
//   - A real Postgres database with Phase 20 migration applied.
//   - LITELLM_TEST_DB_URL environment variable.
//
// Run with:
//
//	go test -tags integration ./apps/control-plane/internal/litellmconfig/...

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/litellmconfig"
	"gopkg.in/yaml.v3"
)

func connectLiteLLMTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LITELLM_TEST_DB_URL")
	if dsn == "" {
		t.Skip("LITELLM_TEST_DB_URL not set; skipping litellmconfig integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connectLiteLLMTestDB: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("connectLiteLLMTestDB ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// integMockRestarter records calls to Restart for integration tests.
// Separate from the unit-test mockRestarter in generator_test.go to avoid
// redeclaration (both live in package litellmconfig_test).
type integMockRestarter struct {
	calls int
	err   error
}

func (m *integMockRestarter) Restart(_ context.Context) error {
	m.calls++
	return m.err
}

// seedSyncProvider inserts a custom_providers row. Cleans up on test exit.
func seedSyncProvider(t *testing.T, pool *pgxpool.Pool, slug string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO public.custom_providers
			(slug, display_name, base_url, api_key_env, litellm_prefix, enabled, created_at, updated_at)
		VALUES ($1, $1, 'https://api.example.com/v1', 'INTEG_TEST_KEY', 'integ/', true, now(), now())
		ON CONFLICT (slug) DO UPDATE SET enabled = true
	`, slug)
	if err != nil {
		t.Fatalf("seedSyncProvider %q: %v", slug, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.custom_providers WHERE slug = $1", slug)
	})
}

// seedSyncRoute inserts a provider_routes row. Cleans up on test exit.
func seedSyncRoute(t *testing.T, pool *pgxpool.Pool, routeID, aliasID, providerSlug, modelID string) {
	t.Helper()
	ctx := context.Background()
	// Ensure the alias_id exists in model_aliases (FK may be enforced).
	_, _ = pool.Exec(ctx, `
		INSERT INTO public.model_aliases
			(alias_id, owned_by, display_name, summary, visibility, lifecycle,
			 capability_badges, input_price_credits, output_price_credits, created_at, updated_at)
		VALUES ($1, 'test', $1, 'test', 'public', 'stable', '[]'::jsonb, 10, 30, now(), now())
		ON CONFLICT (alias_id) DO NOTHING
	`, aliasID)
	_, err := pool.Exec(ctx, `
		INSERT INTO public.provider_routes
			(route_id, alias_id, provider, provider_model, litellm_model_name, price_class, health_state, priority)
		VALUES ($1, $2, $3, $4, $3 || '/' || $4, 'standard', 'healthy', 1)
		ON CONFLICT (route_id) DO NOTHING
	`, routeID, aliasID, providerSlug, modelID)
	if err != nil {
		t.Fatalf("seedSyncRoute %q: %v", routeID, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.provider_routes WHERE route_id = $1", routeID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.model_aliases WHERE alias_id = $1", aliasID)
	})
}

// TestSyncServiceIntegration verifies that SyncService.Sync reads DB rows,
// produces valid YAML, and calls the restarter exactly once.
func TestSyncServiceIntegration(t *testing.T) {
	pool := connectLiteLLMTestDB(t)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	providerSlug := "integ-provider-" + suffix
	routeID1 := "integ-route-a-" + suffix
	routeID2 := "integ-route-b-" + suffix
	aliasID1 := "integ-alias-a-" + suffix
	aliasID2 := "integ-alias-b-" + suffix

	// -------------------------------------------------------------------------
	// Step 1: Seed provider + two routes.
	// -------------------------------------------------------------------------
	seedSyncProvider(t, pool, providerSlug)
	seedSyncRoute(t, pool, routeID1, aliasID1, providerSlug, "model-alpha")
	seedSyncRoute(t, pool, routeID2, aliasID2, providerSlug, "model-beta")

	// -------------------------------------------------------------------------
	// Step 2: Call SyncService.Sync with a MockRestarter.
	// -------------------------------------------------------------------------
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	restarter := &integMockRestarter{}
	svc := litellmconfig.NewSyncService(pool, configPath, "test-master-key", restarter)

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 3: Read written config file; parse YAML; assert model_list length.
	// -------------------------------------------------------------------------
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("YAML parse error: %v", err)
	}

	modelList, ok := parsed["model_list"].([]interface{})
	if !ok {
		t.Fatalf("model_list missing or wrong type: %T", parsed["model_list"])
	}
	if len(modelList) < 2 {
		t.Errorf("expected at least 2 model_list entries (seeded 2 routes), got %d", len(modelList))
	}

	// -------------------------------------------------------------------------
	// Step 4: Assert MockRestarter.Restart was called exactly once.
	// -------------------------------------------------------------------------
	if restarter.calls != 1 {
		t.Errorf("expected Restart called exactly once, got %d", restarter.calls)
	}

	// -------------------------------------------------------------------------
	// Step 5: Assert api_key field uses os.environ/ format (not a literal key).
	// -------------------------------------------------------------------------
	yamlStr := string(data)
	if !strings.Contains(yamlStr, "os.environ/") {
		t.Errorf("expected api_key to use os.environ/ format, got config:\n%s", yamlStr)
	}
	// Confirm no literal key values are embedded.
	if strings.Contains(yamlStr, "INTEG_TEST_KEY") && !strings.Contains(yamlStr, "os.environ/INTEG_TEST_KEY") {
		t.Errorf("api_key must use os.environ/ reference, not literal value")
	}

	t.Logf("TestSyncServiceIntegration: YAML written to %s, %d model entries, restarter called %d time(s)",
		configPath, len(modelList), restarter.calls)
}
