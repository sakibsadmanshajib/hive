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
// (20260824_02_free_pool_router.sql) puts four deployments behind one alias on
// purpose: all four rows share litellm_model_name 'route-free-pool', so
// whichever member SelectRoute picks dispatches the same load-balanced gateway
// group at the alias's single fixed price. The D-032 ambiguity this file guards
// (one price, unclear upstream cost) cannot arise when every member serves
// under one gateway name at one catalog price. A member added or removed
// without updating the expected count fails here loudly.
var expectedMultiRouteAliases = map[string]int{
	"hive-free": 4,
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
