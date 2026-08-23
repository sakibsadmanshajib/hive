package routing

import (
	"math/big"
	"regexp"
	"strings"
	"testing"
)

// Offline guards over the 2026-08-23 free-route repricing migration.
//
// Why a separate file rather than widening catalog_alias_pricing_test.go: that
// file's guards all bottom out in the margin formula, credits = usd_per_million
// * 1.4 * 100000 (the pre-rescale unit; since migration
// 20260823_40_credit_unit_rescale_billion.sql every stored price is 10,000x
// larger), and its parseRate refuses a zero rate outright ("that is a
// mispricing, not a rate"). A free upstream costs zero, so there is no rate to
// derive these two prices from and the formula cannot be the invariant here.
//
// What replaces it is the HALVING RELATION. The owner directed exactly 50
// percent of each alias's current price, so the checkable property is
// new == old / 2 for every unit the alias bills, with the old figures pinned
// below from the migration that set them. That is also what makes these guards
// fail on the OLD rate: a migration that left hive-default at 10500 fails
// TestFreeAliasPricesAreExactlyHalfTheOldRates, because 10500 is not half of
// 10500.
//
// Everything here is positional, for the reason sqlparse_test.go documents:
// presence of a number somewhere in a file is not an assertion. 5250 and 21000
// both appear in this migration, and 21000 is hive-default's NEW output price
// and hive-auto's OLD input price, so a guard that only looked for digits would
// pass with the two aliases' figures swapped.
const freePricingMigrationRelPath = "supabase/migrations/20260823_20_free_route_aliases_half_price.sql"

// The second half of the same owner directive: the remaining Groq TEXT routes
// move to the same free model, and their prices do NOT change. Kept as its own
// file, and guarded separately, precisely so that "no price moves here" is a
// structural property of the file rather than a claim in its header.
const groqFreeMigrationRelPath = "supabase/migrations/20260823_21_groq_text_routes_to_openrouter_free.sql"

// oldRates are the prices in force before this migration, read out of
// supabase/migrations/20260822_02_catalog_alias_restructure.sql step 7, which is
// the statement that set them. Pinned here rather than parsed from that file so
// that a later edit to it cannot silently move the baseline this halving is
// measured against.
var oldRates = map[string]map[string]int64{
	"hive-default": {
		"input_price_credits":       10500,
		"output_price_credits":      42000,
		"cache_read_price_credits":  0,
		"cache_write_price_credits": 0,
	},
	"hive-auto": {
		"input_price_credits":       21000,
		"output_price_credits":      84000,
		"cache_read_price_credits":  0,
		"cache_write_price_credits": 0,
	},
}

// moneyColumns is every column on model_aliases that carries a price. Both
// aliases are price_unit 'tokens', and precedence.go bills prompt tokens at
// input_price_credits and completion tokens at output_price_credits and reads
// neither cache column, but all four are published through
// catalog.CatalogPricing, so all four are covered.
var moneyColumns = []string{
	"input_price_credits",
	"output_price_credits",
	"cache_read_price_credits",
	"cache_write_price_credits",
}

// billedColumns are the two the gateway actually charges from. These are the
// ones that must never reach zero: routing.Service refuses an alias whose input
// and output prices are both zero, and a zero rate on a served request is the
// free-serve shape D-034 exists to prevent.
var billedColumns = []string{"input_price_credits", "output_price_credits"}

// freeRouteByAlias is the route each alias must end up pinned to.
var freeRouteByAlias = map[string]string{
	"hive-default": "route-free-default",
	"hive-auto":    "route-free-auto",
}

// retiredGroqRoutes are the routes this migration must disable, one per alias.
var retiredGroqRoutes = []string{"route-groq-default", "route-groq-auto"}

// halveLineRe parses the migration's own declaration table:
//
//	-- HALVE| alias | field | old | new
//
// Same convention as the DERIVE table in catalog_alias_pricing_test.go, and for
// the same reason: the migration states its arithmetic in a machine-readable
// form so a reader and a test cannot disagree about what it claims.
var halveLineRe = regexp.MustCompile(`(?m)^--\s*HALVE\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*([0-9]+)\s*\|\s*([0-9]+)\s*$`)

type halveRow struct {
	Alias  string
	Field  string
	Old    string
	New    string
	Source string
}

func readFreePricingMigration(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, freePricingMigrationRelPath)
}

// freeMigrationSQL is the migration with all commentary removed. Structural
// assertions run on this, never on the raw text, because the header discusses
// price figures, route ids and `health_state = 'disabled'` at length and a
// guard matching raw text is satisfied by prose.
func freeMigrationSQL(t *testing.T) string {
	t.Helper()
	return stripSQLComments(readFreePricingMigration(t))
}

// parseHalveRows reads the HALVE table out of the RAW migration text. Raw on
// purpose: the table lives in comments, so the stripped form has none of it.
func parseHalveRows(t *testing.T) []halveRow {
	t.Helper()

	matches := halveLineRe.FindAllStringSubmatch(readFreePricingMigration(t), -1)
	if len(matches) == 0 {
		t.Fatalf("%s declares no '-- HALVE|' rows; a repriced column with no declared old value cannot be checked against anything", freePricingMigrationRelPath)
	}

	rows := make([]halveRow, 0, len(matches))
	for _, m := range matches {
		rows = append(rows, halveRow{Alias: m[1], Field: m[2], Old: m[3], New: m[4]})
	}
	return rows
}

// aliasPriceUpdates returns, per alias, the money columns this migration
// assigns and the literal it assigns to each.
func aliasPriceUpdates(t *testing.T) map[string]map[string]string {
	t.Helper()
	return updateAssignments(freeMigrationSQL(t), "public.model_aliases", "alias_id")
}

// TestFreeAliasHalveTableIsArithmeticallyHalf checks the migration's own
// declaration table before anything is compared to the SQL. A HALVE row whose
// new value is not exactly half its old value is a wrong claim, and every other
// guard in this file measures the SQL against these rows.
func TestFreeAliasHalveTableIsArithmeticallyHalf(t *testing.T) {
	for _, row := range parseHalveRows(t) {
		old, ok := new(big.Int).SetString(row.Old, 10)
		if !ok {
			t.Errorf("HALVE row %s/%s: old value %q is not an integer", row.Alias, row.Field, row.Old)
			continue
		}
		got, ok := new(big.Int).SetString(row.New, 10)
		if !ok {
			t.Errorf("HALVE row %s/%s: new value %q is not an integer", row.Alias, row.Field, row.New)
			continue
		}

		// Exact integer halving, and the remainder is checked rather than
		// discarded. Every figure in this migration halves cleanly; a remainder
		// would mean a rounding decision was made silently, which is exactly
		// what D-031 keeps out of the money path.
		want, rem := new(big.Int).QuoRem(old, big.NewInt(2), new(big.Int))
		if rem.Sign() != 0 {
			t.Errorf("HALVE row %s/%s: old value %s does not halve to a whole credit; a rounding rule would have to be stated and is not", row.Alias, row.Field, row.Old)
			continue
		}
		if want.Cmp(got) != 0 {
			t.Errorf("HALVE row %s/%s: claims %s, but half of %s is %s", row.Alias, row.Field, row.New, row.Old, want)
		}
	}
}

// TestFreeAliasHalveTableMatchesTheRatesInForce pins the HALVE table's OLD
// column to the prices that were actually in force. Without this the halving is
// self-referential: a migration could claim any old value it liked and halve it
// correctly while charging whatever it wanted.
func TestFreeAliasHalveTableMatchesTheRatesInForce(t *testing.T) {
	seen := map[string]map[string]bool{}
	for _, row := range parseHalveRows(t) {
		want, ok := oldRates[row.Alias]
		if !ok {
			t.Errorf("HALVE row names alias %q, which this migration has no business repricing", row.Alias)
			continue
		}
		prev, ok := want[row.Field]
		if !ok {
			t.Errorf("HALVE row %s/%s names a column that is not a money column on model_aliases", row.Alias, row.Field)
			continue
		}
		if row.Old != big.NewInt(prev).String() {
			t.Errorf("HALVE row %s/%s claims the old rate was %s; 20260822_02 step 7 set it to %d", row.Alias, row.Field, row.Old, prev)
		}
		if seen[row.Alias] == nil {
			seen[row.Alias] = map[string]bool{}
		}
		seen[row.Alias][row.Field] = true
	}

	// Every money column of both aliases must be declared. A column left out of
	// the table is a column no guard here covers.
	for alias := range oldRates {
		for _, col := range moneyColumns {
			if !seen[alias][col] {
				t.Errorf("no HALVE row for %s/%s; every billed and published price column must declare its halving", alias, col)
			}
		}
	}
}

// TestFreeAliasPricesAreExactlyHalfTheOldRates is the guard the owner directive
// reduces to, and the one that fails on the old rate. It reads the value the
// migration actually ASSIGNS to each money column of each alias and compares it
// to half the rate in force, so a correct HALVE comment above a wrong UPDATE
// below is caught.
func TestFreeAliasPricesAreExactlyHalfTheOldRates(t *testing.T) {
	updates := aliasPriceUpdates(t)

	for alias, prev := range oldRates {
		assigns, ok := updates[alias]
		if !ok {
			t.Errorf("%s reprices no columns on alias %s; the directive halves both aliases", freePricingMigrationRelPath, alias)
			continue
		}
		for _, col := range moneyColumns {
			got, ok := assigns[col]
			if !ok {
				t.Errorf("alias %s: %s is not assigned; an unrepriced column keeps its old rate", alias, col)
				continue
			}
			want := prev[col] / 2
			if got != big.NewInt(want).String() {
				t.Errorf("alias %s: %s = %s, want %d (exactly half of %d)", alias, col, got, want, prev[col])
			}
		}
	}

	// Nothing else may be repriced by this migration. A halving that leaks onto
	// a third alias is a price change nobody asked for and no guard covers.
	for alias := range updates {
		if _, ok := oldRates[alias]; !ok {
			t.Errorf("%s updates alias %q, which is outside the directive's scope", freePricingMigrationRelPath, alias)
		}
	}
}

// TestFreeAliasPricesNeverReachZero is the fail-closed half. Halving a price
// twice more, or a typo dropping a digit, must not produce a free alias: a zero
// on both billed columns makes routing refuse the alias (RouteInfo.HasCostBasis),
// and a zero on one of them serves that token class for nothing, which is the
// shape that served this gateway free for three days in July (D-034).
func TestFreeAliasPricesNeverReachZero(t *testing.T) {
	updates := aliasPriceUpdates(t)

	for alias := range oldRates {
		for _, col := range billedColumns {
			got, ok := updates[alias][col]
			if !ok {
				continue // already reported by the guard above
			}
			value, ok := new(big.Int).SetString(got, 10)
			if !ok {
				t.Errorf("alias %s: %s = %q is not an integer", alias, col, got)
				continue
			}
			if value.Sign() <= 0 {
				t.Errorf("alias %s: %s = %s. A served request would be billed nothing for that token class", alias, col, got)
			}
		}
	}
}

// TestFreeAliasRepricingChangesOnlyTheRate holds invariant 4 of the directive:
// which token classes are billed, and in what unit, must not move. Only the
// rate does. A brief of this shape previously produced a 262x overcharge by
// widening what was billed instead of only the rate.
func TestFreeAliasRepricingChangesOnlyTheRate(t *testing.T) {
	forbidden := []string{"pricing_mode", "price_unit"}

	for alias, assigns := range aliasPriceUpdates(t) {
		for _, col := range forbidden {
			if _, ok := assigns[col]; ok {
				t.Errorf("alias %s: this migration assigns %s. A repricing may move the rate and nothing else; changing the mode or the unit changes WHAT is billed", alias, col)
			}
		}
	}
}

// TestFreeAliasRoutesTargetAFreeOpenRouterModel checks the routing half: each
// alias gets exactly one new OpenRouter route, and the slug is the free variant.
//
// The `:free` suffix is load-bearing rather than cosmetic. Dropping it selects a
// PAID endpoint of the same model, so the alias would be charging a halved price
// against a real out-of-pocket cost, and nothing else in the tree would notice.
func TestFreeAliasRoutesTargetAFreeOpenRouterModel(t *testing.T) {
	sql := freeMigrationSQL(t)

	byRoute := map[string]map[string]string{}
	for _, row := range insertRows(sql, "public.provider_routes") {
		byRoute[row["route_id"]] = row
	}

	for alias, routeID := range freeRouteByAlias {
		row, ok := byRoute[routeID]
		if !ok {
			t.Errorf("%s inserts no route %s for alias %s", freePricingMigrationRelPath, routeID, alias)
			continue
		}
		if row["alias_id"] != alias {
			t.Errorf("route %s is attached to alias %q, want %q", routeID, row["alias_id"], alias)
		}
		if row["provider"] != "openrouter" {
			t.Errorf("route %s provider = %q, want openrouter", routeID, row["provider"])
		}
		model := row["provider_model"]
		if !strings.HasPrefix(model, "openrouter/") {
			t.Errorf("route %s provider_model = %q; LiteLLM strips a leading openrouter/ as its provider selector, so the prefix must be doubled", routeID, model)
		}
		if !strings.HasSuffix(model, ":free") {
			t.Errorf("route %s provider_model = %q; the directive is a FREE model and the :free variant suffix is what selects the zero-priced endpoint", routeID, model)
		}
		// litellm_model_name is the model_name key in deploy/litellm/config.yaml.
		// If it disagrees with route_id the config sync writes an entry nothing
		// dispatches to.
		if row["litellm_model_name"] != routeID {
			t.Errorf("route %s litellm_model_name = %q, want the route id", routeID, row["litellm_model_name"])
		}
	}
}

// TestRetiredGroqRoutesAreDisabledAndRepointed makes sure the old route is taken
// out of service and no policy is left naming it. A policy pointing at a
// disabled route means SelectRoute finds no candidate and the alias 422s.
func TestRetiredGroqRoutesAreDisabledAndRepointed(t *testing.T) {
	sql := freeMigrationSQL(t)

	for _, routeID := range retiredGroqRoutes {
		disabled := false
		for _, stmt := range splitStatements(sql) {
			if disableRe.MatchString(strings.TrimSpace(stmt)) && strings.Contains(stmt, "'"+routeID+"'") {
				disabled = true
				break
			}
		}
		if !disabled {
			t.Errorf("%s does not disable %s; the alias would then have two enabled routes and an ambiguous price", freePricingMigrationRelPath, routeID)
		}
	}

	policies := updateAssignments(sql, "public.alias_route_policies", "alias_id")
	for alias, routeID := range freeRouteByAlias {
		assigns, ok := policies[alias]
		if !ok {
			t.Errorf("alias %s: fallback_order is not repointed, so its policy still names a route this migration disabled", alias)
			continue
		}
		order := assigns["fallback_order"]
		if !strings.Contains(order, routeID) {
			t.Errorf("alias %s: fallback_order = %q, want it to name %s", alias, order, routeID)
		}
		for _, retired := range retiredGroqRoutes {
			if strings.Contains(order, retired) {
				t.Errorf("alias %s: fallback_order still names the disabled route %s", alias, retired)
			}
		}
	}
}

// TestFreeRoutesKeepTheCapabilitiesTheirAliasesServeToday guards the 422 that an
// under-claim produces. Both aliases are pinned to exactly ONE route, so a
// narrower replacement does not withhold a feature: matchesRequestedCapabilities
// drops the only candidate, SelectRoute returns ErrRouteNotEligible and
// writeRoutingError maps it to 422. tools_supported specifically is the column
// PR #206 routes tools, tool_choice and response_format on, and the chosen free
// model was verified live to support all three.
func TestFreeRoutesKeepTheCapabilitiesTheirAliasesServeToday(t *testing.T) {
	required := []string{
		"supports_responses",
		"supports_chat_completions",
		"supports_completions",
		"supports_streaming",
		"supports_reasoning",
		"tools_supported",
	}

	caps := map[string]map[string]string{}
	for _, row := range insertRows(freeMigrationSQL(t), "public.provider_capabilities") {
		caps[row["route_id"]] = row
	}

	for _, routeID := range freeRouteByAlias {
		row, ok := caps[routeID]
		if !ok {
			t.Errorf("%s inserts no provider_capabilities row for %s; the column defaults are all false, so every endpoint would 422", freePricingMigrationRelPath, routeID)
			continue
		}
		for _, flag := range required {
			if !strings.EqualFold(row[flag], "true") {
				t.Errorf("route %s: %s = %q, want true. On a pinned alias an under-claim is a failed request, not a withheld feature", routeID, flag, row[flag])
			}
		}
		// The inverse: claiming embeddings on a chat-only route would put this
		// route in the embedding cascade's candidate set.
		if strings.EqualFold(row["supports_embeddings"], "true") {
			t.Errorf("route %s claims supports_embeddings; this is a chat model", routeID)
		}
	}
}

// TestFreeRouteAutoCarriesTheSoleCapabilityFlagsForward is the same guard
// TestDisablingASoleCapabilityCarrierHandsItsFlagsOn applies to the 2026-08-22
// migration, aimed at this one's target. route-groq-auto inherited
// supports_batch, supports_image_generation and supports_image_edit and is the
// only row in the catalog carrying them. SelectRoute hard-filters on each flag
// and batchstore sends NeedBatch = true for EVERY batch, so disabling that route
// without handing the flags on leaves /v1/batches, /v1/images/generations and
// /v1/images/edits with zero eligible routes for EVERY alias in the system.
func TestFreeRouteAutoCarriesTheSoleCapabilityFlagsForward(t *testing.T) {
	sql := freeMigrationSQL(t)

	disabled := false
	for _, stmt := range splitStatements(sql) {
		if disableRe.MatchString(strings.TrimSpace(stmt)) && strings.Contains(stmt, "'route-groq-auto'") {
			disabled = true
			break
		}
	}
	if !disabled {
		t.Skip("route-groq-auto is not disabled by this migration, so its capabilities are not at risk")
	}

	caps := insertRows(sql, "public.provider_capabilities")
	if len(caps) == 0 {
		t.Fatal("route-groq-auto is disabled but this migration inserts no provider_capabilities rows to hand its flags to")
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
			t.Errorf("this migration disables route-groq-auto, the only route in the catalog carrying %s, and grants that flag to no replacement route. Every endpoint gated on it would find zero eligible routes for every alias.", flag)
		}
	}
}

// ---------------------------------------------------------------------------
// Second half of the directive: the remaining Groq TEXT routes move to the same
// free model, at UNCHANGED prices.
// ---------------------------------------------------------------------------

// groqTextRepoints are the aliases the second migration moves, and the route
// each must end up on. Audio is deliberately absent: hive-stt and hive-tts stay
// on Groq, and TestGroqFreeRepointLeavesAudioOnGroq holds that.
var groqTextRepoints = map[string]string{
	"hive-small":  "route-free-small",
	"hive-medium": "route-free-medium",
	"hive-fast":   "route-free-fast",
}

// retiredGroqTextRoutes are the routes that migration must disable.
var retiredGroqTextRoutes = []string{"route-groq-small", "route-groq-medium", "route-groq-fast"}

// audioRoutes must survive untouched. OpenRouter offers no OpenAI-compatible
// speech endpoint at all and no model advertising selectable voices, and the
// only free models that take audio at all take it as chat input rather than
// through a transcription endpoint, so moving these would remove voice from the
// product rather than migrate it.
var audioRoutes = []string{"route-groq-stt", "route-groq-tts"}

func groqFreeMigrationSQL(t *testing.T) string {
	t.Helper()
	return stripSQLComments(readRepoFile(t, groqFreeMigrationRelPath))
}

// TestGroqFreeRepointTouchesNoPrice is the guard that makes "prices unchanged"
// checkable rather than a promise in a comment. The owner's 50 percent
// instruction covers hive-default and hive-auto only; serving hive-small,
// hive-medium and hive-fast from a free upstream at their existing prices widens
// margin, which is the intended outcome. A price column written here would be
// an unrequested price change on three customer-facing aliases.
func TestGroqFreeRepointTouchesNoPrice(t *testing.T) {
	sql := groqFreeMigrationSQL(t)

	// Two independent checks, because they fail differently. First: no UPDATE of
	// model_aliases at all, which is the strongest form and the one the
	// migration is written to satisfy.
	if updates := updateAssignments(sql, "public.model_aliases", "alias_id"); len(updates) != 0 {
		for alias, assigns := range updates {
			t.Errorf("%s updates model_aliases for %s (%v); this migration must not write that table", groqFreeMigrationRelPath, alias, assigns)
		}
	}

	// Second: no price column name appears in any statement, which also catches
	// an INSERT ... ON CONFLICT DO UPDATE or a shape updateAssignments does not
	// model.
	for _, col := range append(append([]string{}, moneyColumns...), "pricing_mode", "price_unit") {
		if strings.Contains(strings.ToLower(sql), col) {
			t.Errorf("%s mentions %s in an executable statement; the Groq repoint must not move a price", groqFreeMigrationRelPath, col)
		}
	}
}

// TestGroqTextRoutesRepointToTheFreeModel is the routing half: each moved alias
// gets one new OpenRouter route on the free variant, and its policy follows.
func TestGroqTextRoutesRepointToTheFreeModel(t *testing.T) {
	sql := groqFreeMigrationSQL(t)

	byRoute := map[string]map[string]string{}
	for _, row := range insertRows(sql, "public.provider_routes") {
		byRoute[row["route_id"]] = row
	}

	for alias, routeID := range groqTextRepoints {
		row, ok := byRoute[routeID]
		if !ok {
			t.Errorf("%s inserts no route %s for alias %s", groqFreeMigrationRelPath, routeID, alias)
			continue
		}
		if row["alias_id"] != alias {
			t.Errorf("route %s is attached to alias %q, want %q", routeID, row["alias_id"], alias)
		}
		if row["provider"] != "openrouter" {
			t.Errorf("route %s provider = %q, want openrouter", routeID, row["provider"])
		}
		model := row["provider_model"]
		if !strings.HasPrefix(model, "openrouter/") {
			t.Errorf("route %s provider_model = %q; the openrouter/ prefix must be doubled because LiteLLM strips the leading one", routeID, model)
		}
		if !strings.HasSuffix(model, ":free") {
			t.Errorf("route %s provider_model = %q; without the :free variant suffix this selects a PAID endpoint, reintroducing the out-of-pocket spend this migration exists to remove", routeID, model)
		}
		if row["litellm_model_name"] != routeID {
			t.Errorf("route %s litellm_model_name = %q, want the route id", routeID, row["litellm_model_name"])
		}
	}

	policies := updateAssignments(sql, "public.alias_route_policies", "alias_id")
	for alias, routeID := range groqTextRepoints {
		assigns, ok := policies[alias]
		if !ok {
			t.Errorf("alias %s: fallback_order is not repointed, so its policy still names a route this migration disabled", alias)
			continue
		}
		order := assigns["fallback_order"]
		if !strings.Contains(order, routeID) {
			t.Errorf("alias %s: fallback_order = %q, want it to name %s", alias, order, routeID)
		}
		for _, retired := range retiredGroqTextRoutes {
			if strings.Contains(order, retired) {
				t.Errorf("alias %s: fallback_order still names the disabled route %s", alias, retired)
			}
		}
		// policy_mode must not move. hive-fast is 'latency' from its original
		// seed and hive-small and hive-medium are 'pinned'; a repoint has no
		// business changing selection strategy.
		if _, ok := assigns["policy_mode"]; ok {
			t.Errorf("alias %s: this migration assigns policy_mode; a route repoint must not change the selection strategy", alias)
		}
	}
}

// TestGroqTextRoutesAreDisabled holds the other side: the old route is out of
// service, so no alias is left with two enabled routes and an ambiguous price.
func TestGroqTextRoutesAreDisabled(t *testing.T) {
	sql := groqFreeMigrationSQL(t)

	for _, routeID := range retiredGroqTextRoutes {
		disabled := false
		for _, stmt := range splitStatements(sql) {
			if disableRe.MatchString(strings.TrimSpace(stmt)) && strings.Contains(stmt, "'"+routeID+"'") {
				disabled = true
				break
			}
		}
		if !disabled {
			t.Errorf("%s does not disable %s; its alias would then have two enabled routes", groqFreeMigrationRelPath, routeID)
		}
	}
}

// TestGroqFreeRepointLeavesAudioOnGroq is the out-of-scope guard, and it is the
// one with a live product behind it. Groq STT and TTS serve Bengali voice
// dictation, wired to the gateway in PR #1079. OpenRouter has no
// OpenAI-compatible speech endpoint and no model advertising selectable voices,
// so there is nothing to move these to; disabling them would delete voice from
// the product.
func TestGroqFreeRepointLeavesAudioOnGroq(t *testing.T) {
	sql := groqFreeMigrationSQL(t)

	for _, routeID := range audioRoutes {
		if strings.Contains(sql, routeID) {
			t.Errorf("%s names %s in an executable statement; Groq audio is explicitly out of scope and must not be repointed or disabled", groqFreeMigrationRelPath, routeID)
		}
	}
}

// TestRepointedGroqTextRoutesKeepTheirCapabilities carries the parity check onto
// the three moved routes. Two of them keep supports_reasoning true; hive-fast's
// stays false, which is status-quo preservation of a pre-existing under-claim on
// a deprecated alias that 20260822_02 examined and deliberately left alone.
//
// The free model's own parameter list omits reasoning_effort, stop, seed and the
// penalties, so parity was probed live rather than inferred: twelve request
// shapes including all of those, plus json_schema structured output, all
// returned 200. There is no request shape that works today and fails after the
// repoint.
func TestRepointedGroqTextRoutesKeepTheirCapabilities(t *testing.T) {
	// route_id to the flags it must declare, mirroring the rows being replaced.
	want := map[string]map[string]bool{
		"route-free-small": {
			"supports_responses": true, "supports_chat_completions": true,
			"supports_completions": true, "supports_streaming": true,
			"supports_reasoning": true, "tools_supported": true,
			"supports_embeddings": false,
		},
		"route-free-medium": {
			"supports_responses": true, "supports_chat_completions": true,
			"supports_completions": true, "supports_streaming": true,
			"supports_reasoning": true, "tools_supported": true,
			"supports_embeddings": false,
		},
		"route-free-fast": {
			"supports_responses": true, "supports_chat_completions": true,
			"supports_completions": true, "supports_streaming": true,
			"supports_reasoning": false, "tools_supported": true,
			"supports_embeddings": false,
		},
	}

	caps := map[string]map[string]string{}
	for _, row := range insertRows(groqFreeMigrationSQL(t), "public.provider_capabilities") {
		caps[row["route_id"]] = row
	}

	for routeID, flags := range want {
		row, ok := caps[routeID]
		if !ok {
			t.Errorf("%s inserts no provider_capabilities row for %s; every column defaults to false, so every endpoint would 422", groqFreeMigrationRelPath, routeID)
			continue
		}
		for flag, expected := range flags {
			got := strings.EqualFold(row[flag], "true")
			if got != expected {
				t.Errorf("route %s: %s = %v, want %v", routeID, flag, got, expected)
			}
		}
		// None of these three is a sole carrier of a media flag, so none of them
		// may claim one. route-free-auto is where those live.
		for _, flag := range soleCarrierFlags {
			if strings.EqualFold(row[flag], "true") {
				t.Errorf("route %s claims %s; the routes it replaces did not, and only route-free-auto carries the media flags", routeID, flag)
			}
		}
	}
}
