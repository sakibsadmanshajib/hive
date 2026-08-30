package routing

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

// Billing inertness of the free pool, tied to THIS PR's migration.
//
// Rewritten after independent review. The first version of this file was four
// tests that all passed unchanged on main with the migration reverted: two ran
// against stubRepository whose pricing the test itself set (so the magnitude
// asserted was the fixture echoing back), one read the frozen 20260824_02, and
// one was a corpus sweep that passes on any tree where nobody has done the bad
// thing yet. They were guards presented as evidence.
//
// What changed. Every test below either derives its figures from the migration
// corpus rather than from a literal, or declares a precondition that fails when
// 20260830_01 is reverted, or both. Tests that remain guards are named Guard
// and say so.
//
// The property under test, confirmed by the reviewer at service.go:175 and not
// in dispute: pricing resolves through LoadAliasPricing(ctx, aliasID) AFTER
// selection, and provider_routes carries no price column, so which pool member
// answers cannot reach the charge. That property is structural and pre-existing.
// It matters here because 20260830_01 points a member at a ROUTER that selects
// among 21 zero-priced models per request, which is issue #689's exact shape.

const freeModelsRouterUpstream = "openrouter/openrouter/free"

// hiveFreePoolMembers is the pool as the corpus defines it. Listed once so the
// three tests below cannot drift apart on which rows they mean.
var hiveFreePoolMembers = []string{
	"route-free-pool-free",
	"route-free-pool-gemini",
	"route-free-pool-groq",
	"route-free-pool-groq-2",
}

// ─── migration corpus replay ────────────────────────────────────────────────
//
// The catalog's live state is not any one migration, it is the fold of all of
// them in filename order. Reading a single file is what made the previous
// TestFreePoolCarriesNoPerMemberPrice inert: it read 20260824_02, a file this
// PR does not touch.

func migrationFilesInOrder(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "supabase", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		out = append(out, "supabase/migrations/"+e.Name())
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no migrations found; a corpus fold over an empty set proves nothing")
	}
	return out
}

// effectiveRouteColumns folds every migration into the final column values for
// one provider_routes row: the INSERT that created it, then every later UPDATE
// that assigned to it, in filename order.
//
// It REFUSES rather than returning a stale value when a statement names the
// route but the fold could not read it. That guard is the fold's own weak
// point, found in review. updateAssignments only understands an UPDATE whose
// WHERE pins one `route_id = '<literal>'`; a bulk `WHERE route_id IN (...)`, or
// one scoped by alias_id, is invisible to it. Without the guard the fold would
// silently report the pre-update value and every test built on it would go
// green while the live row held something else.
//
// The shape is already in the corpus, so this is not defensive guessing:
// 20260826_01_route_reasoning_reserve.sql raises the other three pool members
// with `where route_id in (...)`. It does not bite today because every
// statement touching route-free-pool-free is single keyed, and it would bite
// the first time somebody repoints this member from inside an IN list.
func effectiveRouteColumns(t *testing.T, routeID string) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, rel := range migrationFilesInOrder(t) {
		sql := stripSQLComments(readRepoFile(t, rel))
		for _, row := range insertRows(sql, "public.provider_routes") {
			if row["route_id"] != routeID {
				continue
			}
			for k, v := range row {
				state[k] = v
			}
		}
		assignsByRoute := updateAssignments(sql, "public.provider_routes", "route_id")
		assertNoUnreadUpdate(t, rel, sql, routeID, assignsByRoute)
		for key, assigns := range assignsByRoute {
			if key != routeID {
				continue
			}
			for k, v := range assigns {
				state[k] = v
			}
		}
	}
	return state
}

// assertNoUnreadUpdate fails when a migration contains an UPDATE on
// provider_routes that mentions routeID but which updateAssignments did not
// key to it. See effectiveRouteColumns for why silence here would be worse
// than a red test.
func assertNoUnreadUpdate(t *testing.T, rel, sql, routeID string, read map[string]map[string]string) {
	t.Helper()
	if _, ok := read[routeID]; ok {
		return
	}
	quoted := "'" + routeID + "'"
	for _, stmt := range splitStatements(sql) {
		if !strings.HasPrefix(strings.ToLower(stmt), "update public.provider_routes") {
			continue
		}
		if !strings.Contains(stmt, quoted) {
			continue
		}
		t.Fatalf("%s updates public.provider_routes and names %s, but the corpus fold could not read that assignment: the WHERE clause is not a single `route_id = '<literal>'` (an IN list or an alias_id scope does this). Extend updateAssignments rather than letting the fold report a stale value. Statement: %.300s", rel, routeID, stmt)
	}
}

// effectiveAliasColumns does the same fold for a model_aliases row.
func effectiveAliasColumns(t *testing.T, aliasID string) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, rel := range migrationFilesInOrder(t) {
		sql := stripSQLComments(readRepoFile(t, rel))
		for _, row := range insertRows(sql, "public.model_aliases") {
			if row["alias_id"] != aliasID {
				continue
			}
			for k, v := range row {
				state[k] = v
			}
		}
		for key, assigns := range updateAssignments(sql, "public.model_aliases", "alias_id") {
			if key != aliasID {
				continue
			}
			for k, v := range assigns {
				state[k] = v
			}
		}
	}
	return state
}

// parseCreditsColumn reads a credits column as a plain integer. A NULL or any
// non-numeric form is fatal rather than coerced, because that is precisely the
// shape a variable-priced alias has and reading it as zero would hide it.
func parseCreditsColumn(t *testing.T, alias map[string]string, column string) int64 {
	t.Helper()
	raw, ok := alias[column]
	if !ok {
		t.Fatalf("hive-free carries no %s in the migration corpus", column)
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		t.Fatalf("hive-free %s is empty in the corpus", column)
	}
	var v int64
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			t.Fatalf("hive-free %s = %q, which is not a plain integer; a NULL here would mean variable pricing", column, raw)
		}
		v = v*10 + int64(r-'0')
	}
	return v
}

func routerBackedMembers(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, routeID := range hiveFreePoolMembers {
		if effectiveRouteColumns(t, routeID)["provider_model"] == freeModelsRouterUpstream {
			out = append(out, routeID)
		}
	}
	return out
}

// ─── tests that go red when 20260830_01 is reverted ─────────────────────────

// TestFreePoolOpenRouterMemberResolvesToTheFreeRouter folds the whole corpus
// rather than reading one file, so it reports what the live row actually holds.
//
// RED WITH THE MIGRATION REVERTED: the fold yields
// openrouter/dots-studio/dots-3-note-preview:free and the first assertion fails.
func TestFreePoolOpenRouterMemberResolvesToTheFreeRouter(t *testing.T) {
	got := effectiveRouteColumns(t, "route-free-pool-free")["provider_model"]

	if got != freeModelsRouterUpstream {
		t.Fatalf("route-free-pool-free effective provider_model = %q, want %q", got, freeModelsRouterUpstream)
	}
	// The defect being removed, stated as a property rather than as one banned
	// string: a vendor-scoped slug carrying :free IS a pin on one model.
	if strings.HasSuffix(got, ":free") {
		t.Errorf("effective provider_model %q still pins one model", got)
	}
}

// TestARouterBackedMemberForcesFixedAliasPricing is the money assertion, and
// its precondition is what makes it evidence rather than a guard.
//
// A router upstream means the model that answers is chosen per request, so the
// alias serving it must not bill from what the upstream reported: that is
// exactly what issue #689 did. The precondition asserts such a member exists,
// so the test cannot pass vacuously.
//
// RED WITH THE MIGRATION REVERTED: no pool member resolves to a router, the
// precondition fails, and the test reports that it had nothing to check.
func TestARouterBackedMemberForcesFixedAliasPricing(t *testing.T) {
	routerBacked := routerBackedMembers(t)
	if len(routerBacked) == 0 {
		t.Fatalf("no hive-free pool member resolves to %s, so this test checked nothing; it exists because a router upstream is what makes the pricing mode below load bearing", freeModelsRouterUpstream)
	}

	alias := effectiveAliasColumns(t, "hive-free")
	if mode, present := alias["pricing_mode"]; present && mode != "fixed" {
		t.Fatalf("hive-free pricing_mode = %q with %v behind a router; upstream_actual would settle from whichever free model happened to answer", mode, routerBacked)
	}
	if in := parseCreditsColumn(t, alias, "input_price_credits"); in <= 0 {
		t.Errorf("hive-free input_price_credits = %d, want positive; a zero-priced alias behind a router bills nothing whatever answers", in)
	}
	if out := parseCreditsColumn(t, alias, "output_price_credits"); out <= 0 {
		t.Errorf("hive-free output_price_credits = %d, want positive", out)
	}
}

// TestChargeBasisIsIdenticalWhicheverPoolMemberAnswers runs the REAL service
// over corpus-derived catalog state.
//
// Three things make this evidence rather than the fixture echo the reviewer
// correctly rejected in the previous version:
//
//  1. The expected magnitude is folded out of the migration corpus, not written
//     as a literal, so it tracks the catalog instead of restating it. Note what
//     that does NOT cover, so a later reader does not misread this as the price
//     pin: wantIn and wantOut are handed to the stub and asserted back out, so
//     both sides of the comparison trace to the same read and a WRONG catalog
//     price would pass here. The literal magnitude pin lives in
//     free_pool_router_test.go's TestHiveFreePriceIsTheSmallServicePrice
//     (1000000 and 4000000). What this test does catch is SelectRoute deriving
//     a price per member, which is the property actually being claimed.
//  2. Every member carries its own effective ProviderModel, so a per-member
//     pricing path would have something to key on and would produce different
//     numbers across the subtests instead of one.
//  3. The negative control below proves these assertions can distinguish two
//     different prices at all, which is what a fixture echo cannot do.
//
// RED WITH THE MIGRATION REVERTED: the precondition fails before any pricing is
// asserted.
func TestChargeBasisIsIdenticalWhicheverPoolMemberAnswers(t *testing.T) {
	if len(routerBackedMembers(t)) == 0 {
		t.Fatalf("no pool member resolves to %s; this test exists to show that a per-request model choice cannot move the charge, and without one there is no per-request choice", freeModelsRouterUpstream)
	}

	alias := effectiveAliasColumns(t, "hive-free")
	wantIn := parseCreditsColumn(t, alias, "input_price_credits")
	wantOut := parseCreditsColumn(t, alias, "output_price_credits")

	seenUpstreams := map[string]bool{}
	for _, routeID := range hiveFreePoolMembers {
		cols := effectiveRouteColumns(t, routeID)
		seenUpstreams[cols["provider_model"]] = true

		t.Run(routeID, func(t *testing.T) {
			repo := &stubRepository{
				policy: catalog.AliasPolicySnapshot{
					AliasID: "hive-free", PolicyMode: "pinned", AllowPriceClassWidening: false,
				},
				candidates: []RouteCandidate{{
					RouteID:                 routeID,
					AliasID:                 "hive-free",
					Provider:                cols["provider"],
					ProviderModel:           cols["provider_model"],
					LiteLLMModelName:        cols["litellm_model_name"],
					PriceClass:              cols["price_class"],
					HealthState:             "healthy",
					Priority:                10,
					SupportsChatCompletions: true,
					SupportsStreaming:       true,
				}},
				pricing: catalog.FixedPricing(wantIn, wantOut),
			}

			result, err := NewService(repo, nil).SelectRoute(context.Background(), SelectionInput{
				AliasID: "hive-free", NeedChatCompletions: true,
			})
			if err != nil {
				t.Fatalf("SelectRoute: %v", err)
			}
			if result.RouteID != routeID {
				t.Fatalf("selected %q, want %q", result.RouteID, routeID)
			}
			if result.Pricing.PricingMode != catalog.PricingModeFixed {
				t.Fatalf("pricing mode = %q, want fixed", result.Pricing.PricingMode)
			}
			assertPrice(t, "input", result.Pricing.InputPriceCredits, wantIn)
			assertPrice(t, "output", result.Pricing.OutputPriceCredits, wantOut)
			if result.Pricing.ReservationEstimateCredits != nil {
				t.Errorf("reservation estimate = %d, want nil on a fixed-price alias", *result.Pricing.ReservationEstimateCredits)
			}
		})
	}

	// The subtests only mean something if the members genuinely differ in what
	// they call upstream. One shared value would make "the price did not move"
	// trivially true.
	if len(seenUpstreams) < 2 {
		t.Errorf("all %d pool members share one upstream model %v; the invariance asserted above is then vacuous", len(hiveFreePoolMembers), seenUpstreams)
	}
}

// TestSelectRouteReportsADifferentAliasPriceDifferently is the negative control
// for the test above. Without it, "every member returned the same number" is
// equally consistent with the assertions being unable to see a difference at
// all, which is the failure mode the reviewer named.
func TestSelectRouteReportsADifferentAliasPriceDifferently(t *testing.T) {
	alias := effectiveAliasColumns(t, "hive-free")
	freeIn := parseCreditsColumn(t, alias, "input_price_credits")

	other := freeIn + 12_345
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{AliasID: "hive-free", PolicyMode: "pinned"},
		candidates: []RouteCandidate{{
			RouteID: "route-free-pool-free", AliasID: "hive-free", Provider: "openrouter",
			ProviderModel: freeModelsRouterUpstream, LiteLLMModelName: "route-free-pool",
			PriceClass: "standard", HealthState: "healthy", Priority: 10,
			SupportsChatCompletions: true,
		}},
		pricing: catalog.FixedPricing(other, other*4),
	}

	result, err := NewService(repo, nil).SelectRoute(context.Background(), SelectionInput{
		AliasID: "hive-free", NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}
	assertPrice(t, "input", result.Pricing.InputPriceCredits, other)
	if result.Pricing.InputPriceCredits != nil && *result.Pricing.InputPriceCredits == freeIn {
		t.Fatal("a deliberately different price came back as the catalog price; the assertions above cannot tell two numbers apart")
	}
}

func assertPrice(t *testing.T, side string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s price is nil, want %d", side, want)
	}
	if *got != want {
		t.Errorf("%s price = %d, want %d (folded from the migration corpus)", side, *got, want)
	}
}

// ─── guards, labelled as such ───────────────────────────────────────────────

// TestNoMigrationPutsHiveFreeOnUpstreamActualGuard is a REGRESSION GUARD, not
// evidence for this change: it passes on any tree where nobody has done the bad
// thing yet, including main with this PR reverted.
//
// It earns its place anyway. hive-auto was flipped from fixed to
// upstream_actual one day after being set (D-059), so the regression it watches
// for has happened once already, and behind a router it would settle every
// request at whichever free model answered.
func TestNoMigrationPutsHiveFreeOnUpstreamActualGuard(t *testing.T) {
	checked := 0
	for _, rel := range migrationFilesInOrder(t) {
		sql := strings.ToLower(stripSQLComments(readRepoFile(t, rel)))
		if !strings.Contains(sql, "hive-free") {
			continue
		}
		checked++
		for _, stmt := range splitStatements(sql) {
			if !strings.Contains(stmt, "hive-free") {
				continue
			}
			if strings.Contains(stmt, "upstream_actual") {
				t.Errorf("%s contains a statement naming both hive-free and upstream_actual: %.200s", rel, stmt)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no migration mentions hive-free, so this guard swept nothing")
	}
}

// TestProviderRoutesNeverGainsAPriceColumnGuard is a REGRESSION GUARD over the
// whole corpus. It replaces the previous single-file version, which read
// 20260824_02 and so could not fail on anything this PR did.
//
// One alias, one price (D-032) is what makes member selection billing inert. A
// price column on provider_routes is the schema change that would end it.
func TestProviderRoutesNeverGainsAPriceColumnGuard(t *testing.T) {
	banned := []string{
		"input_price_credits", "output_price_credits",
		"cache_read_price_credits", "cache_write_price_credits",
		"reservation_estimate_credits", "pricing_mode",
	}
	checked := 0
	for _, rel := range migrationFilesInOrder(t) {
		sql := strings.ToLower(stripSQLComments(readRepoFile(t, rel)))
		for _, stmt := range splitStatements(sql) {
			if !strings.HasPrefix(stmt, "alter table public.provider_routes") {
				continue
			}
			checked++
			for _, col := range banned {
				if strings.Contains(stmt, col) {
					t.Errorf("%s adds %s to provider_routes: %.200s", rel, col, stmt)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no ALTER TABLE public.provider_routes statement found in the corpus; this guard swept nothing")
	}
}
