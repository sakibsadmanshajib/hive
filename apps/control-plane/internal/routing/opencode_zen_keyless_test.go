package routing

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Offline guards over the keyless OpenCode Zen provider added on 2026-08-30.
//
// This is the catalog's first upstream that carries no credential at all. The
// access gate is an HTTP request header, not a key, which makes three things
// silent when broken and is why each is pinned here:
//
//  1. The provider row must be keyless. custom_providers.api_key_env is NOT
//     NULL, so "keyless" is expressed as the empty string, and the config
//     generator reads that as "emit a literal, not an os.environ/ reference"
//     (litellmconfig.KeylessAPIKey). A row that named some other provider's
//     environment variable would quietly send that provider's real key to a
//     third party.
//  2. The route must have its own litellm_model_name, NOT the shared
//     route-free-pool group. Hive filters candidate routes on capability flags
//     but LiteLLM dispatches by group, so a member whose flags differ from its
//     group siblings lets a tool-bearing request land on a deployment that
//     cannot serve it. This route's tools_supported is true and the four
//     existing pool members' is false, so joining that group would build
//     exactly that defect.
//  3. The price must be fixed, never upstream_actual. This upstream reports
//     "cost": "0" on every response, so settling at actual upstream cost would
//     bill nothing for a served request, which D-055 forbids outright and
//     which is the shape of issue #689.
//
// Everything asserted about capability is measured, not copied from the pool
// rows. Evidence is recorded in the pull request body; the short version is
// that chat completions, streaming, native tool_calls and json_schema
// response_format all return 200, while /v1/completions and /v1/embeddings
// return 404 and no response has ever reported a non-zero cached_tokens.

const (
	zenMigrationRelPath = "supabase/migrations/20260830_01_opencode_zen_keyless_provider.sql"
	zenProviderSlug     = "opencode-zen"
	zenRouteID          = "route-free-opencode-zen"
	zenAliasID          = "hive-free-tools"
	zenSeedConfigPath   = "deploy/litellm/config.yaml"
	freePoolSharedGroup = "route-free-pool"
)

func zenMigrationSQL(t *testing.T) string {
	t.Helper()
	return stripSQLComments(readRepoFile(t, zenMigrationRelPath))
}

func zenRow(t *testing.T, table, keyCol, keyVal string) map[string]string {
	t.Helper()
	for _, row := range insertRows(zenMigrationSQL(t), table) {
		if row[keyCol] == keyVal {
			return row
		}
	}
	t.Fatalf("%s inserts no %s row with %s = %q", zenMigrationRelPath, table, keyCol, keyVal)
	return nil
}

// TestOpenCodeZenProviderRowIsKeyless pins the one column that expresses
// "this provider has no credential", and pins the endpoint it points at.
func TestOpenCodeZenProviderRowIsKeyless(t *testing.T) {
	row := zenRow(t, "public.custom_providers", "slug", zenProviderSlug)

	if got := row["api_key_env"]; got != "" {
		t.Errorf("provider %s api_key_env = %q, want the empty string; a named variable here would send some other provider's real key to this third-party endpoint",
			zenProviderSlug, got)
	}
	if got := row["base_url"]; got != "https://opencode.ai/zen/v1" {
		t.Errorf("provider %s base_url = %q, want https://opencode.ai/zen/v1", zenProviderSlug, got)
	}
	if got := row["litellm_prefix"]; got != "openai/" {
		t.Errorf("provider %s litellm_prefix = %q, want openai/; the generic OpenAI adapter plus a non-empty api_base is what makes this prefix mean this endpoint",
			zenProviderSlug, got)
	}
	if got := row["enabled"]; got != "true" {
		t.Errorf("provider %s enabled = %q, want true; a disabled provider emits no model_list entry at all", zenProviderSlug, got)
	}
}

// TestOpenCodeZenRouteKeepsItsOwnLitellmGroup is invariant 2. It is the single
// most important line in this file: sharing route-free-pool would let a
// tool-bearing request that Hive correctly routed here be dispatched by LiteLLM
// to a pool member that rejects tools.
func TestOpenCodeZenRouteKeepsItsOwnLitellmGroup(t *testing.T) {
	row := zenRow(t, "public.provider_routes", "route_id", zenRouteID)

	if got := row["litellm_model_name"]; got != zenRouteID {
		t.Errorf("route %s litellm_model_name = %q, want its own group %q", zenRouteID, got, zenRouteID)
	}
	if row["litellm_model_name"] == freePoolSharedGroup {
		t.Errorf("route %s joined the shared %q group; Hive filters candidates on capability flags but LiteLLM dispatches by group, so a member with different flags lets requests land where they cannot be served",
			zenRouteID, freePoolSharedGroup)
	}
	if got := row["provider"]; got != zenProviderSlug {
		t.Errorf("route %s provider = %q, want %q", zenRouteID, got, zenProviderSlug)
	}
	if got := row["provider_model"]; got != "openai/big-pickle" {
		t.Errorf("route %s provider_model = %q, want openai/big-pickle; it is the only free model id measured at 100 percent, and three of the catalogued ids are dead upstream",
			zenRouteID, got)
	}
	if got := row["alias_id"]; got != zenAliasID {
		t.Errorf("route %s alias_id = %q, want %q", zenRouteID, got, zenAliasID)
	}
	if got := row["health_state"]; got != "healthy" {
		t.Errorf("route %s health_state = %q, want healthy", zenRouteID, got)
	}
}

// TestOpenCodeZenMigrationLeavesTheExistingPoolAlone. Another agent owns the
// four existing members' capability rows concurrently. This migration must not
// write any of them, and must not write the shared group name anywhere.
func TestOpenCodeZenMigrationLeavesTheExistingPoolAlone(t *testing.T) {
	sql := zenMigrationSQL(t)

	if strings.Contains(sql, freePoolSharedGroup) {
		t.Errorf("%s mentions the shared pool group %q; this route is deliberately outside that pool", zenMigrationRelPath, freePoolSharedGroup)
	}

	for _, table := range []string{"public.provider_capabilities", "public.provider_routes"} {
		for _, row := range insertRows(sql, table) {
			if row["route_id"] != zenRouteID {
				t.Errorf("%s writes a %s row for %q; the only route this migration may touch is %s",
					zenMigrationRelPath, table, row["route_id"], zenRouteID)
			}
		}
	}

	if n := len(updateAssignments(sql, "public.provider_capabilities", "route_id")); n != 0 {
		t.Errorf("%s carries %d UPDATE statements against provider_capabilities; it must add its own row and edit nobody else's", zenMigrationRelPath, n)
	}
}

// zenCapabilities are the values MEASURED against the live endpoint on
// 2026-08-30, not the values the existing pool members carry.
var zenCapabilities = map[string]string{
	// Hive translates /v1/responses into a chat-completions body before
	// dispatch (apps/edge-api/internal/inference/responses.go), so this flag
	// describes the Hive surface, and a chat-capable upstream satisfies it.
	"supports_responses": "true",
	// Measured: HTTP 200 on POST /v1/chat/completions.
	"supports_chat_completions": "true",
	// Measured: POST /v1/completions returns 404 (an HTML page, not an API
	// error), so the legacy completions surface does not exist upstream.
	"supports_completions": "false",
	// Measured: POST /v1/embeddings returns 404.
	"supports_embeddings": "false",
	// Measured: a stream:true request returns chat.completion.chunk SSE frames.
	"supports_streaming": "true",
	// Measured: reasoning_effort is accepted with HTTP 200 and the response
	// schema carries reasoning_content. Under-claiming this on a pinned
	// single-route alias would 422 reasoning requests that work today.
	"supports_reasoning": "true",
	// Measured: two identical calls both reported cached_tokens 0 and
	// cache_write_tokens null. Nothing here bills a cache class either
	// (D-055): the alias's cache prices are zero.
	"supports_cache_read":  "false",
	"supports_cache_write": "false",
	// Measured: a tools request returned finish_reason tool_calls with a
	// well-formed native tool_calls array, and a json_schema strict
	// response_format returned schema-valid JSON. This flag gates tools,
	// tool_choice AND response_format (PR #206), and it is the whole reason
	// this alias exists separately from hive-free.
	"tools_supported": "true",
	// Not probed, not claimed. The sole-carrier succession for these three
	// runs through route-openrouter-auto-live and is untouched here.
	"supports_batch":            "false",
	"supports_image_generation": "false",
	"supports_image_edit":       "false",
}

func TestOpenCodeZenCapabilitiesMatchWhatWasMeasured(t *testing.T) {
	row := zenRow(t, "public.provider_capabilities", "route_id", zenRouteID)

	for column, want := range zenCapabilities {
		got, ok := row[column]
		if !ok {
			t.Errorf("provider_capabilities row for %s names no %s column; every unnamed column defaults to false and an under-claim 422s the endpoint",
				zenRouteID, column)
			continue
		}
		if got != want {
			t.Errorf("provider_capabilities.%s = %q for %s, want %q", column, got, zenRouteID, want)
		}
	}
}

// TestOpenCodeZenAliasIsFixedPriced is the money guard. Zero upstream cost
// means upstream_actual would settle every served request at nothing.
func TestOpenCodeZenAliasIsFixedPriced(t *testing.T) {
	row := zenRow(t, "public.model_aliases", "alias_id", zenAliasID)

	if got := row["pricing_mode"]; got != "fixed" {
		t.Errorf("alias %s pricing_mode = %q, want fixed; this upstream reports cost 0 on every response, so upstream_actual would bill nothing for a served request (D-055, issue #689)",
			zenAliasID, got)
	}
	// The same service price hive-free carries (20260824_02): it covers gateway
	// serving cost rather than an upstream rate, because there is no upstream
	// rate to derive a margin from.
	for column, want := range map[string]string{
		"input_price_credits":       "1000000",
		"output_price_credits":      "4000000",
		"cache_read_price_credits":  "0",
		"cache_write_price_credits": "0",
	} {
		if got := row[column]; got != want {
			t.Errorf("alias %s %s = %q, want %q", zenAliasID, column, got, want)
		}
	}
	if got := row["visibility"]; got != "public" {
		t.Errorf("alias %s visibility = %q, want public", zenAliasID, got)
	}
}

// TestOpenCodeZenAliasIsReachable. An alias absent from the policy groups is
// invisible to a default-tier key however correct its price, and a pinned
// policy with the wrong fallback_order names a route that does not exist.
func TestOpenCodeZenAliasIsReachable(t *testing.T) {
	sql := zenMigrationSQL(t)

	groups := map[string]bool{}
	for _, row := range insertRows(sql, "public.model_policy_group_members") {
		if row["alias_id"] == zenAliasID {
			groups[row["group_name"]] = true
		}
	}
	for _, want := range []string{"default", "closed"} {
		if !groups[want] {
			t.Errorf("%s adds no model_policy_group_members row putting %s in the %q group; the alias would be invisible to keys in that group",
				zenMigrationRelPath, zenAliasID, want)
		}
	}

	policy := map[string]string{}
	for _, row := range insertRows(sql, "public.alias_route_policies") {
		if row["alias_id"] == zenAliasID {
			policy = row
		}
	}
	if policy["policy_mode"] != "pinned" {
		t.Errorf("alias %s policy_mode = %q, want pinned; a single-route alias is pinned by construction", zenAliasID, policy["policy_mode"])
	}
	if !strings.Contains(policy["fallback_order"], zenRouteID) {
		t.Errorf("alias %s fallback_order = %q, want it to name %s", zenAliasID, policy["fallback_order"], zenRouteID)
	}
}

// TestOpenCodeZenSeedConfigCarriesTheAccessHeaders is the access mechanism
// where a reader can see it. The User-Agent is the entire gate on this
// upstream: any other value answers 429, so a missing header here is a route
// that looks correct in the database and serves nothing.
//
// It lives in deploy/litellm/config.yaml rather than in the migration because
// the config generator owns exactly three litellm_params keys (model, api_base,
// api_key) and merges every other key from this file, field by field. That is
// the same seam route-openrouter-auto-live's extra_body uses.
func TestOpenCodeZenSeedConfigCarriesTheAccessHeaders(t *testing.T) {
	var seed struct {
		ModelList []struct {
			ModelName    string `yaml:"model_name"`
			LiteLLMParam struct {
				Model        string            `yaml:"model"`
				APIBase      string            `yaml:"api_base"`
				APIKey       string            `yaml:"api_key"`
				ExtraHeaders map[string]string `yaml:"extra_headers"`
			} `yaml:"litellm_params"`
		} `yaml:"model_list"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, zenSeedConfigPath)), &seed); err != nil {
		t.Fatalf("parse %s: %v", zenSeedConfigPath, err)
	}

	for _, entry := range seed.ModelList {
		if entry.ModelName != zenRouteID {
			continue
		}
		headers := entry.LiteLLMParam.ExtraHeaders
		if got := headers["User-Agent"]; got != "opencode" {
			t.Errorf("%s entry %s extra_headers User-Agent = %q, want \"opencode\"; every other value answers 429 FreeUsageLimitError",
				zenSeedConfigPath, zenRouteID, got)
		}
		auth, present := headers["Authorization"]
		if !present {
			t.Errorf("%s entry %s sets no Authorization override; the api_key literal would reach the upstream as a bearer token and be rejected 401",
				zenSeedConfigPath, zenRouteID)
		} else if auth != "" {
			t.Errorf("%s entry %s Authorization override = %q, want the empty string; any non-empty bearer token is rejected 401",
				zenSeedConfigPath, zenRouteID, auth)
		}
		if got := entry.LiteLLMParam.APIBase; got != "https://opencode.ai/zen/v1" {
			t.Errorf("%s entry %s api_base = %q, want https://opencode.ai/zen/v1", zenSeedConfigPath, zenRouteID, got)
		}
		if got := entry.LiteLLMParam.Model; got != "openai/big-pickle" {
			t.Errorf("%s entry %s model = %q, want openai/big-pickle", zenSeedConfigPath, zenRouteID, got)
		}
		return
	}

	t.Fatalf("%s carries no model_list entry named %s; without it the sync emits the route with no access headers and every request answers 429",
		zenSeedConfigPath, zenRouteID)
}
