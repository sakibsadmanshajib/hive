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
		// A skip is green, which is exactly how this suite went unnoticed
		// for issue #701: gated behind this same variable, never wired into
		// any workflow, always skipped, never once failing CI. In CI, an
		// absent variable must fail loudly instead of vanishing quietly.
		// GitHub Actions sets CI=true on every runner; a local dev run
		// (CI unset) still skips.
		if os.Getenv("CI") != "" {
			t.Fatal("LITELLM_TEST_DB_URL not set in CI; this suite must not silently skip (issue #701)")
		}
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

// mapKeys returns the keys of m, for use in test failure messages only.
func mapKeys(m map[string]map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
	// provider_model is seeded pre-prefixed (provider + "/" + modelID), matching
	// every real migration row (e.g. "openrouter/openai/gpt-4o-mini"). A bare,
	// unprefixed provider_model here would certify the exact shape the
	// deleted litellm_prefix concatenation existed to repair (issue #701
	// review) instead of the real invariant SyncService now depends on.
	_, err := pool.Exec(ctx, `
		INSERT INTO public.provider_routes
			(route_id, alias_id, provider, provider_model, litellm_model_name, price_class, health_state, priority)
		VALUES ($1, $2, $3, $3 || '/' || $4, $3 || '/' || $4, 'standard', 'healthy', 1)
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
	// Step 3b: Assert every seeded active route actually made it into the
	// generated config, keyed on model_name (route_id) with the exact
	// litellm_params.model the route was seeded with (provider_model). A
	// future rename of either source column (the defect this test guards
	// against, issue #701) makes this fail loudly — either the query errors
	// before reaching this point, or the seeded route_id/provider_model pair
	// goes missing/wrong here — instead of silently shipping an empty or
	// mismatched model_list.
	// -------------------------------------------------------------------------
	byModelName := map[string]map[string]interface{}{}
	for _, item := range modelList {
		entry, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("model_list entry has unexpected type: %T", item)
		}
		name, ok := entry["model_name"].(string)
		if !ok {
			t.Fatalf("model_list entry missing model_name: %#v", entry)
		}
		byModelName[name] = entry
	}

	wantRoutes := map[string]string{
		routeID1: providerSlug + "/model-alpha",
		routeID2: providerSlug + "/model-beta",
	}
	for routeID, wantProviderModel := range wantRoutes {
		entry, ok := byModelName[routeID]
		if !ok {
			t.Fatalf("seeded route %q missing from generated model_list; got model_names: %v", routeID, mapKeys(byModelName))
		}
		params, ok := entry["litellm_params"].(map[string]interface{})
		if !ok {
			t.Fatalf("route %q: litellm_params missing or wrong type: %#v", routeID, entry["litellm_params"])
		}
		gotModel, _ := params["model"].(string)
		if gotModel != wantProviderModel {
			t.Errorf("route %q: litellm_params.model = %q, want %q (provider_model)", routeID, gotModel, wantProviderModel)
		}
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

// TestSyncRemovesRetiredRouteFromExistingConfig is the live-DB half of the
// accretion rule. The unit test with the same shape can only prove mergeConfig
// honours KnownRouteIDs; it passes even if SyncService never populates them.
// This one reads a really retired route out of the migration chain
// (20260801_01_alias_pricing_correction.sql set route-openrouter-fast-fallback
// to health_state 'disabled' and kept the row), plants an entry for it in the
// existing config, and requires the sync to drop it while keeping an entry that
// has no provider_routes row at all.
func TestSyncRemovesRetiredRouteFromExistingConfig(t *testing.T) {
	pool := connectLiteLLMTestDB(t)
	ctx := context.Background()

	const retiredRoute = "route-openrouter-fast-fallback"
	var healthState string
	if err := pool.QueryRow(ctx,
		`SELECT health_state FROM public.provider_routes WHERE route_id = $1`,
		retiredRoute).Scan(&healthState); err != nil {
		t.Fatalf("read retired %s row: %v", retiredRoute, err)
	}
	if healthState != "disabled" && healthState != "eol" {
		t.Fatalf("%s is %q; this test needs a route that is retired but still present in provider_routes", retiredRoute, healthState)
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	existing := "model_list:\n" +
		"  - model_name: " + retiredRoute + "\n" +
		"    litellm_params:\n" +
		"      model: openrouter/openai/gpt-4o-mini\n" +
		"      api_key: os.environ/OPENROUTER_API_KEY\n" +
		"  - model_name: integ-operator-managed-entry\n" +
		"    litellm_params:\n" +
		"      model: openai/some-local-model\n" +
		"      api_base: http://localhost:11434/v1\n" +
		"      api_key: \"none\"\n" +
		"general_settings:\n" +
		"  master_key: old-key\n"
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := litellmconfig.NewSyncService(pool, configPath, "test-master-key", &integMockRestarter{})
	if err := svc.Sync(ctx); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("YAML parse error: %v", err)
	}
	modelList, _ := parsed["model_list"].([]interface{})
	names := map[string]bool{}
	for _, item := range modelList {
		if entry, ok := item.(map[string]interface{}); ok {
			if name, _ := entry["model_name"].(string); name != "" {
				names[name] = true
			}
		}
	}

	if names[retiredRoute] {
		t.Errorf("%s is retired in provider_routes but its model_list entry survived the sync; nothing can ever remove it", retiredRoute)
	}
	if !names["integ-operator-managed-entry"] {
		t.Errorf("an entry with no provider_routes row must be preserved (issue #705); got model_names: %v", names)
	}
}

// TestSyncKeepsEmbeddingAdapterForSeededRoute proves issue #707 gap 1 against
// the real seeded route, no synthetic fixture: LiteLLM's native `openrouter/`
// provider does not map the /embeddings endpoint, so
// deploy/litellm/config.yaml section 5 calls the same upstream through the
// generic `openai/` adapter with an explicit api_base pointed at OpenRouter.
// route-openrouter-embedding HAS a provider_routes row, so it is DB-managed
// and rewritten on every sync; before this fix the sync emitted the native
// `openrouter/` prefix straight from provider_routes.provider_model and broke
// RAG embeddings on the next sync.
//
// The DB column stays routing-canonical (`openrouter/...`) on purpose: it is
// also read by the price catalog and by deploy-demo-box.yml's "Assert model
// catalog prices agree with the model LiteLLM will call" step, which
// canonicalizes a live `openai/X` + openrouter.ai api_base back to
// `openrouter/X` before comparing against this very column. Storing the
// adapter form in the DB would break that comparison, so the adapter is the
// generator's business, not the database's. This test asserts both halves.
func TestSyncKeepsEmbeddingAdapterForSeededRoute(t *testing.T) {
	pool := connectLiteLLMTestDB(t)
	ctx := context.Background()

	const embeddingRoute = "route-openrouter-embedding"

	var dbModel, healthState string
	err := pool.QueryRow(ctx, `
		SELECT provider_model, health_state
		  FROM public.provider_routes
		 WHERE route_id = $1
	`, embeddingRoute).Scan(&dbModel, &healthState)
	if err != nil {
		t.Fatalf("read seeded %s row: %v", embeddingRoute, err)
	}
	if healthState == "disabled" || healthState == "eol" {
		t.Fatalf("%s is %q in the migration chain; the seeded embedding primary must stay dispatchable", embeddingRoute, healthState)
	}
	// Pins design decision: the DB keeps the routing-canonical provider prefix.
	if !strings.HasPrefix(dbModel, "openrouter/") {
		t.Fatalf("provider_routes.provider_model for %s = %q, want the routing-canonical openrouter/ form; deploy-demo-box.yml's price assertion compares against this column", embeddingRoute, dbModel)
	}
	wantModel := "openai/" + strings.TrimPrefix(dbModel, "openrouter/")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	restarter := &integMockRestarter{}
	svc := litellmconfig.NewSyncService(pool, configPath, "test-master-key", restarter)
	if err := svc.Sync(ctx); err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

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

	var params map[string]interface{}
	for _, item := range modelList {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := entry["model_name"].(string); name == embeddingRoute {
			params, _ = entry["litellm_params"].(map[string]interface{})
			break
		}
	}
	if params == nil {
		t.Fatalf("%s missing from the synced model_list (or has no litellm_params)", embeddingRoute)
	}

	gotModel, _ := params["model"].(string)
	if strings.HasPrefix(gotModel, "openrouter/") {
		t.Errorf("%s: litellm_params.model = %q; LiteLLM's native openrouter/ provider does not map /embeddings, a sync must not emit it (issue #707)", embeddingRoute, gotModel)
	}
	if gotModel != wantModel {
		t.Errorf("%s: litellm_params.model = %q, want %q (generic openai/ adapter over the canonical DB value %q)", embeddingRoute, gotModel, wantModel, dbModel)
	}
	// The api_base is what makes `openai/` mean OpenRouter here, both for
	// LiteLLM at runtime and for the deploy-time price assertion's
	// canonicalization.
	gotBase, _ := params["api_base"].(string)
	if !strings.Contains(gotBase, "openrouter.ai") {
		t.Errorf("%s: litellm_params.api_base = %q, want the OpenRouter base URL from custom_providers", embeddingRoute, gotBase)
	}
}
