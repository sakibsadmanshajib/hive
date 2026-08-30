package routing

import (
	"strings"
	"testing"
)

// Offline guards over the 2026-08-24 free pool router migration.
//
// Three things are pinned here, all positional in the style sqlparse_test.go
// documents:
//
//  1. The pool shape: four provider_routes rows sharing ONE
//     litellm_model_name, which is what makes the config sync emit four
//     LiteLLM deployments under one model_name and what makes an exhausted
//     key fail over instead of taking the alias down.
//  2. The money: hive-free's small service price (a price NOT derived from a
//     provider rate, because every pool member costs zero, so the DERIVE
//     margin formula cannot apply), hive-default's move onto the deepseek
//     rates already in the catalog carried through the rescale factor, and
//     hive-auto's switch to pricing_mode 'upstream_actual' with NULL prices
//     and a bounded hold.
//  3. Part C of the directive: CI and daily automated consumption point at
//     hive-free rather than a paid or dots-free alias.

const freePoolMigrationRelPath = "supabase/migrations/20260824_02_free_pool_router.sql"

const freePoolGroupName = "route-free-pool"

// freePoolMembers are the four deployments this migration seeded under one
// group name, keyed to the provider slug each row routes through.
//
// Still four here because this map describes what 20260824_02 wrote. The live
// pool is three: 20260830_03_free_pool_capability_truth.sql disables
// route-free-pool-free, whose upstream became a per-request router that cannot
// honour the group's capability claim. The live membership is asserted by
// TestFreePoolIsUniformlyToolCapable.
var freePoolMembers = map[string]string{
	"route-free-pool-free":   "openrouter",
	"route-free-pool-gemini": "gemini",
	"route-free-pool-groq":   "groq",
	"route-free-pool-groq-2": "groq-2",
}

func freePoolMigrationSQL(t *testing.T) string {
	t.Helper()
	return stripSQLComments(readRepoFile(t, freePoolMigrationRelPath))
}

func freePoolRouteRows(t *testing.T) map[string]map[string]string {
	t.Helper()
	byRoute := map[string]map[string]string{}
	for _, row := range insertRows(freePoolMigrationSQL(t), "public.provider_routes") {
		byRoute[row["route_id"]] = row
	}
	return byRoute
}

// TestFreePoolRoutesShareOneLitellmModelName is the load-balancing invariant.
// litellm_model_name is the model_name key in deploy/litellm/config.yaml and
// the only string dispatch addresses; N rows sharing it is the whole router.
func TestFreePoolRoutesShareOneLitellmModelName(t *testing.T) {
	byRoute := freePoolRouteRows(t)

	for routeID, wantProvider := range freePoolMembers {
		row, ok := byRoute[routeID]
		if !ok {
			t.Errorf("%s inserts no pool member route %s", freePoolMigrationRelPath, routeID)
			continue
		}
		if row["provider"] != wantProvider {
			t.Errorf("route %s provider = %q, want %q", routeID, row["provider"], wantProvider)
		}
		if row["litellm_model_name"] != freePoolGroupName {
			t.Errorf("route %s litellm_model_name = %q, want the shared group name %q; a divergent name would leave that deployment out of the load-balanced pool", routeID, row["litellm_model_name"], freePoolGroupName)
		}
		if row["health_state"] != "healthy" {
			t.Errorf("route %s health_state = %q, want healthy; every key slot must serve from birth", routeID, row["health_state"])
		}
	}

	// Exactly four rows carry the group name. A fifth sharing it silently joins
	// the pool; one missing shrinks it without any error anywhere.
	shared := 0
	for _, row := range insertRows(freePoolMigrationSQL(t), "public.provider_routes") {
		if row["litellm_model_name"] == freePoolGroupName {
			shared++
		}
	}
	if shared != len(freePoolMembers) {
		t.Errorf("%d inserted rows share the group name %q, want exactly %d", shared, freePoolGroupName, len(freePoolMembers))
	}

	// Every member references the alias: provider_routes.alias_id is NOT NULL
	// with a non-deferrable FK to model_aliases (20260331_02), so a NULL member
	// would abort this migration on apply.
	for routeID := range freePoolMembers {
		if got := byRoute[routeID]["alias_id"]; got != "hive-free" {
			t.Errorf("pool member %s carries alias_id %q; the column is NOT NULL and every member belongs to hive-free", routeID, got)
		}
	}

	// Statement order: the alias row must be inserted BEFORE the routes that
	// reference it. Index comparison over the comment-stripped SQL.
	lower := strings.ToLower(freePoolMigrationSQL(t))
	aliasAt := strings.Index(lower, "insert into public.model_aliases")
	routesAt := strings.Index(lower, "insert into public.provider_routes")
	if aliasAt < 0 || routesAt < 0 || aliasAt > routesAt {
		t.Errorf("the hive-free alias insert must precede the provider_routes inserts (non-deferrable FK); alias at %d, routes at %d", aliasAt, routesAt)
	}
}

// TestFreePoolUpstreamModelsAreTheVerifiedOnes pins each member's upstream so a
// typo cannot quietly change what the pool serves or what it costs.
func TestFreePoolUpstreamModelsAreTheVerifiedOnes(t *testing.T) {
	want := map[string]string{
		"route-free-pool-free":   "openrouter/dots-studio/dots-3-note-preview:free",
		"route-free-pool-gemini": "openai/gemini-flash-latest",
		"route-free-pool-groq":   "groq/openai/gpt-oss-20b",
		"route-free-pool-groq-2": "groq/openai/gpt-oss-20b",
	}
	byRoute := freePoolRouteRows(t)

	for routeID, wantModel := range want {
		row, ok := byRoute[routeID]
		if !ok {
			t.Errorf("no pool member route %s", routeID)
			continue
		}
		if row["provider_model"] != wantModel {
			t.Errorf("route %s provider_model = %q, want %q", routeID, row["provider_model"], wantModel)
		}
	}

	// HISTORICAL, and no longer live intent. This file pins what 20260824_02
	// inserted, and that migration is frozen, so the pinned model and its
	// load-bearing :free suffix are still asserted here. What the route holds
	// TODAY is openrouter/openrouter/free, OpenRouter's own Free Models
	// Router, set by 20260830_01_openrouter_free_models_router.sql because a
	// pinned free model gets rate limited or retired without notice. Live
	// intent is guarded in openrouter_free_models_router_test.go; do not read
	// the assertion below as the current value.
	dots := byRoute["route-free-pool-free"]["provider_model"]
	if !strings.HasSuffix(dots, ":free") {
		t.Errorf("pool's OpenRouter member %q lost its :free suffix; that selects a PAID endpoint", dots)
	}

	// The Gemini member goes through the generic openai/ adapter, which the
	// generator refuses to emit without a non-empty api_base; the base comes
	// from the custom_providers row checked below.
	gemini := byRoute["route-free-pool-gemini"]
	if !strings.HasPrefix(gemini["provider_model"], "openai/") {
		t.Errorf("gemini member provider_model = %q, want the openai/ adapter form the verified live path uses", gemini["provider_model"])
	}
}

// TestFreePoolKeySlotsAreEnvNamed proves credentials travel as ENV NAMES only.
// No key VALUE may ever appear in this migration; the two new slots must name
// their environment variables explicitly.
func TestFreePoolKeySlotsAreEnvNamed(t *testing.T) {
	sql := freePoolMigrationSQL(t)

	bySlug := map[string]map[string]string{}
	for _, row := range insertRows(sql, "public.custom_providers") {
		bySlug[row["slug"]] = row
	}

	wantEnv := map[string]string{
		"groq-2": "GROQ_API_KEY_2",
		"gemini": "GEMINI_API_KEY",
	}
	for slug, envName := range wantEnv {
		row, ok := bySlug[slug]
		if !ok {
			t.Errorf("%s inserts no custom_providers row for slug %q", freePoolMigrationRelPath, slug)
			continue
		}
		if row["api_key_env"] != envName {
			t.Errorf("custom_providers %q api_key_env = %q, want %q", slug, row["api_key_env"], envName)
		}
	}

	// The gemini base_url is what makes the generic openai/ adapter mean
	// Google's official OpenAI-compatible endpoint rather than OpenAI itself;
	// pointing at api.openai.com would send GEMINI_API_KEY upstream to the
	// wrong vendor, which is exactly the failure the generator's guard exists
	// for.
	if base := bySlug["gemini"]["base_url"]; !strings.Contains(base, "generativelanguage.googleapis.com") {
		t.Errorf("custom_providers 'gemini' base_url = %q, want Google's Generative Language endpoint", base)
	}
}

// TestHiveFreePriceIsTheSmallServicePrice pins the owner-directed service
// price: 1,000,000 credits input / 4,000,000 output per million tokens, i.e.
// $0.001 / $0.004 at the current 1 USD = 1e9 credits unit. There is no DERIVE
// row for these figures and there cannot be one: parseRate rejects a $0.00
// rate ("a mispricing, not a rate"), and every pool upstream costs zero. This
// price covers gateway serving cost; it is asserted literally instead.
func TestHiveFreePriceIsTheSmallServicePrice(t *testing.T) {
	var row map[string]string
	for _, r := range insertRows(freePoolMigrationSQL(t), "public.model_aliases") {
		if r["alias_id"] == "hive-free" {
			row = r
		}
	}
	if row == nil {
		t.Fatalf("%s inserts no hive-free alias", freePoolMigrationRelPath)
	}

	want := map[string]string{
		"input_price_credits":       "1000000",
		"output_price_credits":      "4000000",
		"cache_read_price_credits":  "0",
		"cache_write_price_credits": "0",
	}
	for col, expect := range want {
		if got := strings.TrimSpace(row[col]); got != expect {
			t.Errorf("hive-free %s = %q, want %s", col, got, expect)
		}
	}

	// Fail-closed: a zero on a billed column would serve that token class for
	// nothing (the D-034 shape).
	for _, col := range billedColumns {
		if got := strings.TrimSpace(row[col]); got == "0" {
			t.Errorf("hive-free %s = 0; routing refuses an unpriced alias, but if it ever served, it would bill nothing", col)
		}
	}
}

// TestHiveAutoMovesToUpstreamActual checks the variable-pricing flip: mode
// upstream_actual, all four fixed price columns NULL, and a real hold. The
// hold matches the internal openrouter-auto alias after the rescale (200000 x
// 10000); the shape CHECK from 20260822_30 rejects any other combination.
func TestHiveAutoMovesToUpstreamActual(t *testing.T) {
	assigns, ok := updateAssignments(freePoolMigrationSQL(t), "public.model_aliases", "alias_id")["hive-auto"]
	if !ok {
		t.Fatalf("%s writes no model_aliases row for hive-auto", freePoolMigrationRelPath)
	}

	if got := strings.Trim(strings.TrimSpace(assigns["pricing_mode"]), "'"); got != "upstream_actual" {
		t.Errorf("hive-auto pricing_mode = %s, want 'upstream_actual'", got)
	}
	for _, col := range []string{
		"input_price_credits",
		"output_price_credits",
		"cache_read_price_credits",
		"cache_write_price_credits",
	} {
		if got := strings.TrimSpace(assigns[col]); !strings.EqualFold(got, "null") {
			t.Errorf("hive-auto %s = %s, want null; a stale number under upstream_actual is displayed as a price nothing charges", col, got)
		}
	}
	if got := strings.TrimSpace(assigns["reservation_estimate_credits"]); got != "2000000000" {
		t.Errorf("hive-auto reservation_estimate_credits = %s, want 2000000000 (the rescaled openrouter-auto hold)", got)
	}
}

// TestHiveDefaultRepriceMatchesTheDeepseekRatesAlreadyInCatalog carries the
// 2026-08-22 DERIVE figures through the 10,000x rescale rather than fetching
// anything new: same upstream model deepseek-v4-flash already serves, same
// rate card, current unit.
func TestHiveDefaultRepriceMatchesTheDeepseekRatesAlreadyInCatalog(t *testing.T) {
	assigns, ok := updateAssignments(freePoolMigrationSQL(t), "public.model_aliases", "alias_id")["hive-default"]
	if !ok {
		t.Fatalf("%s reprices no columns on hive-default", freePoolMigrationRelPath)
	}

	// DERIVE figures from 20260822_02 for deepseek-v4-flash, multiplied by the
	// 20260823_40 rescale factor of 10,000 into the current unit.
	want := map[string]string{
		"input_price_credits":       "89460000",
		"output_price_credits":      "178920000",
		"cache_read_price_credits":  "17900000",
		"cache_write_price_credits": "0",
	}
	for col, expect := range want {
		got := strings.TrimSpace(assigns[col])
		if got == "" {
			t.Errorf("hive-default %s is not assigned by this migration", col)
			continue
		}
		if got != expect {
			t.Errorf("hive-default %s = %s, want %s (pre-rescale figure x 10000)", col, got, expect)
		}
	}
}

// TestRetiredFreeRoutesAreDisabledAndPoliciesFollow makes sure no alias is left
// pointing at a disabled route and no policy names one either.
func TestRetiredFreeRoutesAreDisabledAndPoliciesFollow(t *testing.T) {
	sql := freePoolMigrationSQL(t)

	retired := []string{"route-free-auto", "route-free-default"}
	for _, routeID := range retired {
		disabled := false
		for _, stmt := range splitStatements(sql) {
			if disableRe.MatchString(strings.TrimSpace(stmt)) && strings.Contains(stmt, "'"+routeID+"'") {
				disabled = true
				break
			}
		}
		if !disabled {
			t.Errorf("%s does not disable %s; its alias would have two enabled routes and an ambiguous price", freePoolMigrationRelPath, routeID)
		}
	}

	pinned := map[string]string{
		"hive-auto":    "route-openrouter-auto-live",
		"hive-default": "route-deepseek-v4-flash-default",
	}
	policies := updateAssignments(sql, "public.alias_route_policies", "alias_id")
	for alias, routeID := range pinned {
		assigns, ok := policies[alias]
		if !ok {
			t.Errorf("alias %s: fallback_order is not repointed", alias)
			continue
		}
		order := assigns["fallback_order"]
		if !strings.Contains(order, routeID) {
			t.Errorf("alias %s: fallback_order = %q, want it to name %s", alias, order, routeID)
		}
		for _, retiredRoute := range retired {
			if strings.Contains(order, retiredRoute) {
				t.Errorf("alias %s: fallback_order still names disabled route %s", alias, retiredRoute)
			}
		}
	}

	// hive-free is a NEW alias, so its policy arrives as an INSERT rather than
	// an UPDATE; the same no-disabled-route rule applies.
	var freeOrder string
	for _, row := range insertRows(sql, "public.alias_route_policies") {
		if row["alias_id"] == "hive-free" {
			freeOrder = row["fallback_order"]
		}
	}
	if !strings.Contains(freeOrder, "route-free-pool-free") {
		t.Errorf("hive-free policy fallback_order = %q, want it to name route-free-pool-free", freeOrder)
	}
	for _, retiredRoute := range retired {
		if strings.Contains(freeOrder, retiredRoute) {
			t.Errorf("hive-free policy names disabled route %s", retiredRoute)
		}
	}
}

// TestFreePoolDisablingRouteFreeAutoHandsOnTheSoleCarrierFlags is this
// migration's instance of the sole-carrier rule: route-free-auto is today the
// only catalog carrier of the batch/image flags, and disabling it without a
// successor deletes /v1/batches and both image endpoints for every alias.
func TestFreePoolDisablingRouteFreeAutoHandsOnTheSoleCarrierFlags(t *testing.T) {
	sql := freePoolMigrationSQL(t)

	disabled := false
	for _, stmt := range splitStatements(sql) {
		if disableRe.MatchString(strings.TrimSpace(stmt)) && strings.Contains(stmt, "'route-free-auto'") {
			disabled = true
			break
		}
	}
	if !disabled {
		t.Skip("route-free-auto is not disabled by this migration, so its capabilities are not at risk")
	}

	caps := insertRows(sql, "public.provider_capabilities")
	if len(caps) == 0 {
		t.Fatal("route-free-auto is disabled but this migration grants its flags to no replacement capabilities row")
	}
	for _, flag := range soleCarrierFlags {
		granted := false
		for _, row := range caps {
			if strings.EqualFold(row[flag], "true") {
				granted = true
				break
			}
		}
		if !granted {
			t.Errorf("this migration disables route-free-auto, the only route carrying %s, and grants it to no replacement; that endpoint dies catalog-wide", flag)
		}
	}
}

// TestPoolCapabilitiesAsSeededByThisMigration reads what THIS FILE inserted,
// not what the catalog now holds, and the distinction is load-bearing.
//
// This migration seeded tools_supported false on all four members, recording
// that cross-provider parity had never been probed. That was honest about the
// evidence and wrong about the models: the column also gates response_format
// (PR #206), so hive-free answered its own 400 to every structured-output
// request for six days while three of the four members served it fine.
//
// The members were probed on 2026-08-30 and the pool is now uniformly
// tool-capable, with the OpenRouter member retired from it, by
// 20260830_03_free_pool_capability_truth.sql. The EFFECTIVE values are asserted
// by TestFreePoolIsUniformlyToolCapable, which folds the whole migration chain.
// The assertions below deliberately stay file-scoped: they pin what this
// migration wrote, so a future edit to this file cannot quietly change history
// out from under the correction that followed it.
//
// supports_reasoning stays true (an under-claim on a pinned alias is a 422, not
// a withheld feature), and embeddings stay false (chat models).
func TestPoolCapabilitiesAsSeededByThisMigration(t *testing.T) {
	caps := map[string]map[string]string{}
	for _, row := range insertRows(freePoolMigrationSQL(t), "public.provider_capabilities") {
		caps[row["route_id"]] = row
	}

	for routeID := range freePoolMembers {
		row, ok := caps[routeID]
		if !ok {
			t.Errorf("%s inserts no provider_capabilities row for %s; column defaults are all false and every endpoint would 422", freePoolMigrationRelPath, routeID)
			continue
		}
		if strings.EqualFold(row["tools_supported"], "true") {
			t.Errorf("pool route %s claims tools_supported in THIS migration; it seeded false, and the correction lives in 20260830_03_free_pool_capability_truth.sql where the per-member evidence is recorded. Rewriting history here detaches that correction from its reason", routeID)
		}
		if !strings.EqualFold(row["supports_reasoning"], "true") {
			t.Errorf("pool route %s drops supports_reasoning; reasoning requests against the pinned alias would 422", routeID)
		}
		if strings.EqualFold(row["supports_embeddings"], "true") {
			t.Errorf("pool route %s claims supports_embeddings; this is a chat pool", routeID)
		}
		for _, flag := range soleCarrierFlags {
			if strings.EqualFold(row[flag], "true") {
				t.Errorf("pool route %s claims media flag %s; those live on the auto-router successor, not in the pool", routeID, flag)
			}
		}
	}

	// The deepseek mirror keeps the tool parity hive-default is moved here for.
	ds := caps["route-deepseek-v4-flash-default"]
	if ds == nil {
		t.Fatalf("no capabilities row for route-deepseek-v4-flash-default")
	}
	if !strings.EqualFold(ds["tools_supported"], "true") {
		t.Errorf("route-deepseek-v4-flash-default loses tools_supported; hive-default's tool-parity story dies")
	}
	if !strings.EqualFold(ds["supports_cache_read"], "true") {
		t.Errorf("route-deepseek-v4-flash-default drops supports_cache_read; its source row declares it true")
	}
}

// ---------------------------------------------------------------------------
// PART C guards: CI and automated consumption defaults point at hive-free.
// ---------------------------------------------------------------------------

var citestDefaultChecks = []struct {
	name     string
	relPath  string
	mustHave string
	mustNot  string
}{
	{
		name:     "ci.yml live-integration default",
		relPath:  ".github/workflows/ci.yml",
		mustHave: `vars.CI_LIVE_INTEGRATION_MODEL || 'hive-free'`,
		mustNot:  `vars.CI_LIVE_INTEGRATION_MODEL || 'deepseek-v4-pro'`,
	},
	{
		name:     "deploy-demo-box sdk-replay default",
		relPath:  ".github/workflows/deploy-demo-box.yml",
		mustHave: "HIVE_TEST_MODEL: hive-free",
		mustNot:  "HIVE_TEST_MODEL: hive-default",
	},
	{
		name:     "js chat-completions fallback",
		relPath:  "packages/sdk-tests/js/tests/chat-completions/chat-completions.test.ts",
		mustHave: `?? "hive-free"`,
		mustNot:  `?? "hive-default"`,
	},
	{
		name:     "python chat-completions fallback",
		relPath:  "packages/sdk-tests/python/tests/test_chat_completions.py",
		mustHave: `"hive-free")`,
		mustNot:  `"hive-default")`,
	},
	{
		name:     "post-deploy-verify default",
		relPath:  "scripts/post-deploy-verify.py",
		mustHave: `os.environ.get("HIVE_VERIFY_MODEL", "hive-free")`,
		mustNot:  `os.environ.get("HIVE_VERIFY_MODEL", "hive-default")`,
	},
}

// TestCITestDefaultsPointAtTheFreeAlias holds part C of the directive where it
// can be checked offline: every automated consumption surface defaults to
// hive-free, and none still defaults to a paid or dots-only alias. The
// CI_LIVE_INTEGRATION_MODEL repository variable remains the override knob in
// every case.
func TestCITestDefaultsPointAtTheFreeAlias(t *testing.T) {
	for _, tc := range citestDefaultChecks {
		body := readRepoFile(t, tc.relPath)
		if !strings.Contains(body, tc.mustHave) {
			t.Errorf("%s: %q is missing from %s", tc.name, tc.mustHave, tc.relPath)
		}
		if tc.mustNot != "" && strings.Contains(body, tc.mustNot) {
			t.Errorf("%s: %q is still present in %s", tc.name, tc.mustNot, tc.relPath)
		}
	}
}
