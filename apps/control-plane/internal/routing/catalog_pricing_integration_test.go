//go:build integration

package routing

// Integration tests asserting the SEEDED catalog data, not the Go logic.
//
// These are the data half of the issue #617 correction: service_test.go
// proves SelectRoute behaves correctly given a one-route, correctly-priced
// alias, and this file proves the migration chain actually produces one.
//
// Prerequisites:
//   - A Postgres database with the FULL supabase/migrations chain applied,
//     including 20260801_01_alias_pricing_correction.sql and
//     20260801_14_route_groq_fast_cheapest_model.sql.
//   - ROUTING_TEST_DB_URL pointing at it.
//
// Run with:
//
//	go test -tags integration ./apps/control-plane/internal/routing/...

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func connectCatalogDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ROUTING_TEST_DB_URL")
	if dsn == "" {
		// A skipped test and a passing test are indistinguishable inside a
		// green check (issue #655). This suite guards the corrected catalog
		// prices from #617/#651, so in CI a missing DSN is a wiring defect,
		// not a reason to quietly pass. Gated on CI rather than the
		// variable itself so a local developer run can still skip.
		if os.Getenv("CI") != "" {
			t.Fatal("ROUTING_TEST_DB_URL not set in CI: this suite guards the corrected catalog prices (#617) and must not silently pass as skipped")
		}
		t.Skip("ROUTING_TEST_DB_URL not set; skipping integration test (set CI=true to make this fail instead of skip)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connectCatalogDB: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connectCatalogDB ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// pendingMultiRouteAliases are aliases known to violate the one-alias
// one-route rule and deliberately left alone by
// 20260801_01_alias_pricing_correction.sql, pending a decision.
//
// hive-embedding-default carries three routes: two healthy OpenRouter routes
// and one nvidia_nim route at health_state 'eol'. Repointing an embedding
// alias is not a like-for-like swap the way a chat model is -- D-001 ties
// the embedding model to a provisioned pgvector column width, so changing
// which route serves it can invalidate already-stored vectors. That call
// belongs to the owner, so it is reported rather than taken here.
//
// Removing an entry from this map is the entire change once a decision
// lands. Anything NOT listed here is held to the rule.
var pendingMultiRouteAliases = map[string]string{
	"hive-embedding-default": "issue #617 follow-up: embedding route choice is coupled to the provisioned vector width (D-001)",
}

// expectedMultiRouteAliases are aliases that legitimately carry MORE than one
// enabled route, with the EXACT count required. The free pool
// (20260824_02_free_pool_router.sql) puts several deployments behind one alias
// on purpose: every row shares litellm_model_name 'route-free-pool', so
// whichever member SelectRoute picks dispatches the same load-balanced gateway
// group at the alias's single fixed price. The D-032 ambiguity this file guards
// (one price, unclear upstream cost) cannot arise when every member serves
// under one gateway name at one catalog price. A member added or removed
// without updating the expected count fails here loudly.
//
// Two since 20260830_03_free_pool_capability_truth.sql, down from four, and the
// count is the whole point rather than an incidental. Dispatch addresses the
// GROUP and not the route, so a group can only declare what its weakest member
// supports, and owner decision #1563 makes hive-free ONE endpoint. That leaves
// one way to declare tools_supported honestly: every member must have been
// measured to support it.
//
// Two members failed that bar for different reasons and left on the same
// standard. The OpenRouter member was repointed by #1554 at the Free Models
// Router, which picks among the zero-priced catalog per request, and of the 20
// zero-priced models only 10 support response_format at all. The Gemini member
// is documented capable by Google but was never measured against the same
// strict-schema shape, and cannot be from this repository, since its key exists
// only as a CI secret; issue #1566 separately records it capped at 20 requests
// a day with 435 rate-limit failures in 48 hours.
//
// The remaining two are Groq key slots on qwen/qwen3.8-27b, both live-probed.
var expectedMultiRouteAliases = map[string]int{
	"hive-free": 2,
}

// TestSeededAliasHasExactlyOneEnabledRoute enforces the owner's rule: one
// alias maps to exactly one route. An alias with two selectable routes is
// ambiguous about what a request actually costs, which is what made
// hive-fast mispriced in the first place (it carried an OpenRouter route and
// a Groq route whose real costs differ by roughly ten times).
//
// "Enabled" mirrors SelectRoute's own filter, which excludes only
// health_state 'disabled'. Note this is DELIBERATELY not the litellmconfig
// sync filter, which excludes both 'disabled' and 'eol' -- see
// TestNoRouteIsSelectableButUnservable below.
func TestSeededAliasHasExactlyOneEnabledRoute(t *testing.T) {
	pool := connectCatalogDB(t)

	rows, err := pool.Query(context.Background(), `
		SELECT a.alias_id, count(r.route_id)
		FROM public.model_aliases a
		LEFT JOIN public.provider_routes r
		  ON r.alias_id = a.alias_id AND r.health_state <> 'disabled'
		GROUP BY a.alias_id
		ORDER BY a.alias_id
	`)
	if err != nil {
		t.Fatalf("query enabled route counts: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var aliasID string
		var enabled int
		if err := rows.Scan(&aliasID, &enabled); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if reason, pending := pendingMultiRouteAliases[aliasID]; pending {
			if enabled == 1 {
				t.Errorf("alias %s now has exactly 1 enabled route; drop it from pendingMultiRouteAliases (%s)", aliasID, reason)
			}
			continue
		}
		if want, multi := expectedMultiRouteAliases[aliasID]; multi {
			if enabled != want {
				t.Errorf("alias %s has %d enabled routes, want exactly %d (the free pool's member rows; update expectedMultiRouteAliases if the pool changed)", aliasID, enabled, want)
			}
			continue
		}
		if enabled != 1 {
			t.Errorf("alias %s has %d enabled routes, want exactly 1", aliasID, enabled)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if seen == 0 {
		t.Fatal("no aliases found; is the migration chain applied?")
	}
}

// TestHiveFastIsPinnedToOneRouteAtItsUnchangedPrice is requirement (c) at the
// data level: hive-fast has exactly one enabled route, and its price is the
// figure the catalog derived for it.
//
// The price figures below are the 20260818-era derivation rescaled to the
// current credit unit by migration 20260823_40 (factor 10,000, owner
// directive 2026-08-23: 1 USD is now 1e9 credits). Originally they came from
// Groq's published rate for openai/gpt-oss-20b
// (20260818_01_revert_hive_fast_groq_model_decommissioned.sql):
//
//	input:  0.075 USD/M * 1.4 = 0.105 USD/M * 100_000 credits/USD = 10_500
//	output: 0.300 USD/M * 1.4 = 0.420 USD/M * 100_000 credits/USD = 42_000
//
// and the rescale multiplied every stored figure by 10,000 without changing
// any real USD amount:
//
// The ROUTE moved on 2026-08-23
// (20260823_21_groq_text_routes_to_openrouter_free.sql): hive-fast, hive-small
// and hive-medium left Groq for an OpenRouter free model on an owner directive,
// to stop the Groq allowance being consumed. The price deliberately did NOT
// move with it. Only hive-default and hive-auto were repriced, by the companion
// migration 20260823_20, and this alias was not in that instruction's scope.
//
// So these two figures are no longer derivable from the upstream's cost, which
// is zero, and they must not be "corrected" to match it: a zero price makes an
// alias unselectable (RouteInfo.HasCostBasis) rather than free. They are held
// here as the DB-level guard that the repoint did not quietly reprice three
// customer-facing aliases. Any later price change has to fail this test and be
// re-derived rather than drift silently, exactly as before.
func TestHiveFastIsPinnedToOneRouteAtItsUnchangedPrice(t *testing.T) {
	pool := connectCatalogDB(t)

	var routeID, provider, providerModel, healthState string
	err := pool.QueryRow(context.Background(), `
		SELECT route_id, provider, provider_model, health_state
		FROM public.provider_routes
		WHERE alias_id = 'hive-fast' AND health_state <> 'disabled'
	`).Scan(&routeID, &provider, &providerModel, &healthState)
	if err != nil {
		t.Fatalf("hive-fast must have exactly one enabled route: %v", err)
	}
	if provider != "openrouter" {
		t.Errorf("hive-fast provider = %q, want openrouter", provider)
	}
	// The `:free` suffix is the whole point of the repoint: without it the same
	// slug resolves to a PAID endpoint, so the alias would charge an unchanged
	// price against a real out-of-pocket cost.
	if providerModel != "openrouter/dots-studio/dots-3-note-preview:free" {
		t.Errorf("hive-fast provider_model = %q, want openrouter/dots-studio/dots-3-note-preview:free", providerModel)
	}

	var input, output int64
	if err := pool.QueryRow(context.Background(), `
		SELECT input_price_credits, output_price_credits
		FROM public.model_aliases WHERE alias_id = 'hive-fast'
	`).Scan(&input, &output); err != nil {
		t.Fatalf("read hive-fast pricing: %v", err)
	}
	const hiveFastRescaleFactor = 10_000 // migration 20260823_40
	if input != 10_500*hiveFastRescaleFactor {
		t.Errorf("hive-fast input_price_credits = %d, want %d", input, 10_500*hiveFastRescaleFactor)
	}
	if output != 42_000*hiveFastRescaleFactor {
		t.Errorf("hive-fast output_price_credits = %d, want %d", output, 42_000*hiveFastRescaleFactor)
	}
}

// TestNoRouteIsSelectableButUnservable catches a mismatch between two
// filters that both exist today and disagree: SelectRoute excludes only
// health_state 'disabled', while litellmconfig's SyncService excludes
// 'disabled' AND 'eol'. A route left at 'eol' is therefore still selectable
// by routing but is never written into LiteLLM's config, so selecting it
// dispatches to a model the gateway does not serve.
func TestNoRouteIsSelectableButUnservable(t *testing.T) {
	pool := connectCatalogDB(t)

	rows, err := pool.Query(context.Background(), `
		SELECT route_id, alias_id, health_state
		FROM public.provider_routes
		WHERE health_state = 'eol'
		ORDER BY route_id
	`)
	if err != nil {
		t.Fatalf("query eol routes: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var routeID, aliasID, healthState string
		if err := rows.Scan(&routeID, &aliasID, &healthState); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if _, pending := pendingMultiRouteAliases[aliasID]; pending {
			t.Logf("known pending: route %s (alias %s) is 'eol' and still selectable by routing", routeID, aliasID)
			continue
		}
		t.Errorf("route %s (alias %s) is health_state 'eol': selectable by routing but excluded from LiteLLM sync", routeID, aliasID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
}

// TestEveryAliasHasACostBasis is the data-level companion to
// TestSelectRouteRefusesUnpricedAlias: after the correction no alias may
// have both prices at zero, because SelectRoute now refuses to select one
// and the alias would be dead rather than free.
func TestEveryAliasHasACostBasis(t *testing.T) {
	pool := connectCatalogDB(t)

	rows, err := pool.Query(context.Background(), `
		SELECT alias_id
		FROM public.model_aliases
		WHERE input_price_credits = 0 AND output_price_credits = 0
		ORDER BY alias_id
	`)
	if err != nil {
		t.Fatalf("query unpriced aliases: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var aliasID string
		if err := rows.Scan(&aliasID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Errorf("alias %s has no cost basis (both prices zero) and is therefore not selectable", aliasID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
}

// TestHiveFreeResolvesInASeededDatabase is the regression guard for the CI
// live-integration outage of 2026-08-24 (PR #1121): the throwaway database
// carried the free pool rows, yet every plain-chat suite call failed with
// Invalid model name, because nothing had derived LiteLLM's serving config
// from this catalog. The data half is guarded here: the alias the suites call
// by default must resolve to ACTIVE pool members under the exact activity rule
// litellmconfig.SyncService uses (health_state not disabled/eol on an enabled
// provider), so a migration that seeds the rows but leaves them unselectable,
// or a repoint that strands the pinned policy on a dead member, fails in the
// go-tests job instead of only in a live-keyed run. The wiring half (LiteLLM
// actually being told about the group) is asserted in ci.yml's live-integration
// job right after its config sync.
func TestHiveFreeResolvesInASeededDatabase(t *testing.T) {
	pool := connectCatalogDB(t)

	// The alias itself must exist and be publicly selectable.
	var visibility, lifecycle string
	err := pool.QueryRow(context.Background(), `
		SELECT visibility, lifecycle
		FROM public.model_aliases
		WHERE alias_id = 'hive-free'
	`).Scan(&visibility, &lifecycle)
	if err != nil {
		t.Fatalf("hive-free missing from the seeded catalog: %v", err)
	}
	if visibility != "public" || lifecycle != "stable" {
		t.Errorf("hive-free visibility=%q lifecycle=%q, want public/stable", visibility, lifecycle)
	}

	// Active members under SyncService's own filter, which is stricter than
	// SelectRoute's: it also excludes 'eol' and requires the provider enabled.
	rows, err := pool.Query(context.Background(), `
		SELECT pr.route_id, pr.litellm_model_name
		FROM public.provider_routes pr
		JOIN public.custom_providers cp ON cp.slug = pr.provider
		WHERE pr.alias_id = 'hive-free'
		  AND pr.health_state NOT IN ('disabled', 'eol')
		  AND cp.enabled
		ORDER BY pr.route_id
	`)
	if err != nil {
		t.Fatalf("query active hive-free routes: %v", err)
	}
	defer rows.Close()

	const wantGroup = "route-free-pool"
	var members []string
	for rows.Next() {
		var routeID, modelName string
		if err := rows.Scan(&routeID, &modelName); err != nil {
			t.Fatalf("scan: %v", err)
		}
		members = append(members, routeID)
		if modelName != wantGroup {
			t.Errorf("pool member %s serves group %q, want %q", routeID, modelName, wantGroup)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(members) == 0 {
		t.Fatal("hive-free has no active route: the sync would emit no deployment for it and every plain-chat request would fail with Invalid model name")
	}

	// The pinned fallback must point at one of those active members.
	var pinned string
	err = pool.QueryRow(context.Background(), `
	 SELECT fallback_order->>0
	 FROM public.alias_route_policies
	 WHERE alias_id = 'hive-free'
	`).Scan(&pinned)
	if err != nil {
		t.Fatalf("read hive-free pinned policy: %v", err)
	}
	pinnedActive := false
	for _, m := range members {
		if m == pinned {
			pinnedActive = true
			break
		}
	}
	if !pinnedActive {
		t.Errorf("hive-free pins fallback_order[0]=%q, which is not among the active members %v; selection would strand on a dead route", pinned, members)
	}

	// Group membership rows gate tenant visibility: a default-tier key sees
	// only aliases in its groups. Missing membership makes the alias invisible
	// however correct everything else is.
	var groupCount int
	if err := pool.QueryRow(context.Background(), `
	 SELECT count(*) FROM public.model_policy_group_members WHERE alias_id = 'hive-free'
	`).Scan(&groupCount); err != nil {
		t.Fatalf("query hive-free group membership: %v", err)
	}
	if groupCount == 0 {
		t.Error("hive-free belongs to no model_policy_group, so no default-tier tenant can see it")
	}
}
