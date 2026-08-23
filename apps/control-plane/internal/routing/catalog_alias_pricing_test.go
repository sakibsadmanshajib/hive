package routing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Offline guards over the catalog restructure migration.
//
// Why these exist: nothing in the running system re-derives a credit figure, so
// a wrong one is a silent mispricing that ships. Issues #689 and #965 were both
// that shape. CI holds no provider API keys, so the rates are pinned in a
// committed snapshot taken when the migration was written.
//
// Why they parse rather than grep: the first version of this file asserted that
// a credit figure appeared SOMEWHERE in the migration text. Review proved three
// mispricings that passed it. Every one of them is now a positional assertion
// against a named column of a named row:
//
//   - hive-default's reprice changed to hive-medium's figures. Both numbers were
//     already elsewhere in the file, so a 100 percent overcharge on the
//     no-model-specified default alias stayed green.
//   - input and output swapped inside one alias tuple. Both numbers present, green.
//   - a route repointed at a different upstream model with the price untouched,
//     a 2x undercharge, green, because provider_model was checked against the
//     rate snapshot but never against the SQL.
//
// Presence of digits in a file is not the assertion. A value in a column of a
// row is.
//
// KNOWN LIMIT, stated rather than left to be discovered. This whole file is
// pinned to ONE migration filename. The next migration that reprices these
// aliases inherits none of these guards, and will pass this suite while doing
// anything it likes to the catalog. Two ways out when that day comes: key the
// pricing checks off every migration that writes input_price_credits, or move
// the assertions to the database level where catalog_pricing_integration_test.go
// already lives. Neither is done here, because a guard that scans every
// migration has to cope with the historical ones that predate the DERIVE
// convention entirely.
const (
	pricingMigrationRelPath = "supabase/migrations/20260822_02_catalog_alias_restructure.sql"
	providerRatesRelPath    = "testdata/provider_rates_2026-08-22.json"

	// creditsPerUSD mirrors apps/control-plane/internal/payments/types.go.
	// marginNum/marginDen express 1.4 exactly. Integers only, fed to math/big,
	// so no float64 touches a money figure.
	creditsPerUSD = 100000
	marginNum     = 14
	marginDen     = 10
)

// deriveField maps the DERIVE table's field name to the model_aliases column
// the figure must actually land in.
var deriveField = map[string]string{
	"in":         "input_price_credits",
	"out":        "output_price_credits",
	"cache_read": "cache_read_price_credits",
}

type deriveRow struct {
	Alias         string
	RouteID       string
	ProviderModel string
	Field         string
	USD           string
	Credits       string
}

type providerRate struct {
	ProviderModel string  `json:"provider_model"`
	In            string  `json:"usd_in_per_million"`
	Out           string  `json:"usd_out_per_million"`
	CacheRead     *string `json:"usd_cache_read_per_million"`
}

type providerRatesFixture struct {
	FetchedUTC string         `json:"fetched_utc"`
	Models     []providerRate `json:"models"`
}

// decimalRe constrains a DERIVE rate to a plain decimal. big.Rat.SetString also
// accepts ratio, exponent and hex-float forms, so "1/0.5" or "0x1p-3" would
// otherwise parse as a valid provider rate.
var decimalRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// isoDateRe pins fetched_utc to a full calendar date, which is what the two
// substring checks in loadProviderRates need in order to compare anything.
var isoDateRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// parseRate turns a documented provider rate into an exact rational, rejecting
// anything that is not a plain positive decimal.
//
// big.Rat parses a decimal string without loss, so "0.01278" is 1278/100000 and
// not the nearest float64 to it. That is the one rate in this migration whose
// product is fractional, so it is the row a float would round differently.
//
// It returns an error rather than calling t.Fatalf, because the callers loop
// over every DERIVE row with t.Errorf and continue: one malformed rate must not
// abort the run and hide every remaining row, which is the difference between
// a review that reports one problem and one that reports all of them.
func parseRate(usd string) (*big.Rat, error) {
	if !decimalRe.MatchString(usd) {
		return nil, fmt.Errorf("rate %q is not a plain decimal; big.Rat also accepts ratio, exponent and hex-float forms, which are not prices", usd)
	}
	rate, ok := new(big.Rat).SetString(usd)
	if !ok {
		return nil, fmt.Errorf("rate %q is not a valid decimal", usd)
	}
	// A zero or negative rate is a mispricing, not a derivation. Without this,
	// a DERIVE row claiming 0 USD and 0 credits satisfies every assertion here
	// as a correctly derived free model.
	if rate.Sign() <= 0 {
		return nil, fmt.Errorf("rate %q is zero or negative; that is a mispricing, not a rate", usd)
	}
	return rate, nil
}

// expectedCredits computes ceil(usd * MARGIN * CREDITS_PER_USD) exactly.
func expectedCredits(rate *big.Rat) *big.Int {
	product := new(big.Rat).Mul(rate, big.NewRat(marginNum*creditsPerUSD, marginDen))
	quotient, remainder := new(big.Int).QuoRem(product.Num(), product.Denom(), new(big.Int))
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func readPricingMigration(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, pricingMigrationRelPath)
}

// migrationSQL is the migration with all commentary removed. Every structural
// assertion runs on this, never on the raw text: the migration's header
// discusses `lifecycle = 'deprecated'` and `delete from public.model_aliases`
// at length, and a guard matching raw text is satisfied or tripped by prose.
func migrationSQL(t *testing.T) string {
	t.Helper()
	return stripSQLComments(readPricingMigration(t))
}

func loadProviderRates(t *testing.T) map[string]providerRate {
	t.Helper()

	// Go runs tests with the working directory set to the package directory, so
	// the testdata path is relative and the package stays movable.
	body, err := os.ReadFile(providerRatesRelPath)
	if err != nil {
		t.Fatalf("read provider rate snapshot: %v", err)
	}

	var fixture providerRatesFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatalf("parse provider rate snapshot: %v", err)
	}
	if len(fixture.Models) == 0 {
		t.Fatal("provider rate snapshot is empty; it is the only offline record of what these prices were derived from")
	}

	// The snapshot date must match the migration it backs. Repricing against a
	// stale snapshot is the failure this catches, deterministically and with no
	// wall-clock comparison.
	//
	// The shape is checked before the two comparisons, because strings.Contains
	// reports true for an empty substring. A snapshot that omits fetched_utc, or
	// sets it to "", satisfies both checks below while comparing nothing, and so
	// does a one-character value such as "2", which occurs in both of these
	// paths anyway. That is a guard structurally incapable of failing, which is
	// worse than no guard at all, because it reads as coverage.
	if !isoDateRe.MatchString(fixture.FetchedUTC) {
		t.Fatalf("provider rate snapshot has fetched_utc %q; want a full YYYY-MM-DD date. An empty or one-character value passes both substring checks below without comparing anything.", fixture.FetchedUTC)
	}
	if !strings.Contains(providerRatesRelPath, fixture.FetchedUTC) {
		t.Errorf("snapshot fetched_utc %q does not match its own filename %q; the rates and the file have drifted apart", fixture.FetchedUTC, providerRatesRelPath)
	}
	if !strings.Contains(pricingMigrationRelPath, strings.ReplaceAll(fixture.FetchedUTC, "-", "")) {
		t.Errorf("snapshot fetched_utc %q does not match the migration date in %q; these prices were derived from rates fetched for a different migration", fixture.FetchedUTC, pricingMigrationRelPath)
	}

	byModel := make(map[string]providerRate, len(fixture.Models))
	for _, m := range fixture.Models {
		// A duplicate carrying two different rates is exactly the drift this
		// fixture exists to detect, and a map would silently last-wins it.
		if _, dup := byModel[m.ProviderModel]; dup {
			t.Fatalf("provider rate snapshot lists %q twice; one of the two rates is wrong and a map would silently pick one", m.ProviderModel)
		}
		byModel[m.ProviderModel] = m
	}
	return byModel
}

var deriveLineRe = regexp.MustCompile(`(?m)^--\s*DERIVE\|([^\n]*)$`)

// disableRe matches a statement that retires a route. Compiled once at package
// level rather than per call.
var disableRe = regexp.MustCompile(`(?is)^update\s+public\.provider_routes\s+set\s+health_state\s*=\s*'disabled'`)

func parseDeriveRows(t *testing.T, migration string) []deriveRow {
	t.Helper()

	matches := deriveLineRe.FindAllStringSubmatch(migration, -1)
	if len(matches) == 0 {
		t.Fatalf("%s declares no '-- DERIVE|' rows; every price in this repo must show its derivation", pricingMigrationRelPath)
	}

	var rows []deriveRow
	for _, m := range matches {
		fields := strings.Split(m[1], "|")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if len(fields) > 0 && strings.EqualFold(fields[0], "alias_id") {
			continue // column header
		}
		if len(fields) != 6 {
			t.Fatalf("malformed DERIVE row %q: want 6 pipe-separated fields (alias|route|provider_model|field|usd|credits), got %d", m[1], len(fields))
		}
		if _, ok := deriveField[fields[3]]; !ok {
			t.Fatalf("DERIVE row %q has unknown field %q; want in, out or cache_read", m[1], fields[3])
		}
		rows = append(rows, deriveRow{
			Alias:         fields[0],
			RouteID:       fields[1],
			ProviderModel: fields[2],
			Field:         fields[3],
			USD:           fields[4],
			Credits:       fields[5],
		})
	}
	if len(rows) == 0 {
		t.Fatal("DERIVE table has a header but no data rows")
	}
	return rows
}

// pricedAliases returns every alias whose input or output price this migration
// WRITES, whether by INSERT or by UPDATE, mapped to the column values written.
//
// Covering the UPDATE side is load bearing. hive-default and hive-auto are
// repriced rather than inserted, which put them outside every guard keyed on
// the INSERT block, and they are the two highest-value rows in the file.
func pricedAliases(t *testing.T, sql string) map[string]map[string]string {
	t.Helper()

	out := map[string]map[string]string{}
	for _, row := range insertRows(sql, "public.model_aliases") {
		alias := row["alias_id"]
		if alias == "" {
			t.Fatalf("a model_aliases INSERT tuple has no alias_id: %#v", row)
		}
		out[alias] = row
	}
	for alias, assigns := range updateAssignments(sql, "public.model_aliases", "alias_id") {
		// Either price counts. Testing input alone let an
		// `UPDATE ... SET output_price_credits = N` through: the alias landed in
		// neither TestEveryPricedAliasHasCompleteDerivation nor
		// TestDeclaredCreditsLandOnTheirOwnAliasRow, so an output reprice with no
		// DERIVE row behind it passed the whole suite. On an alias this migration
		// also INSERTs, the merge below is what binds the later value to the
		// assertion; without it the INSERT's superseded figure is the one checked,
		// and the figure customers are actually charged goes unexamined.
		_, writesIn := assigns["input_price_credits"]
		_, writesOut := assigns["output_price_credits"]
		if !writesIn && !writesOut {
			continue // e.g. the hive-fast lifecycle marker, which touches no price
		}
		if existing, ok := out[alias]; ok {
			for k, v := range assigns {
				existing[k] = v
			}
			continue
		}
		out[alias] = assigns
	}
	if len(out) == 0 {
		t.Fatal("parsed no priced aliases out of the migration; the parser and the file have diverged and every guard below is checking nothing")
	}
	return out
}

// TestCatalogAliasPricesMatchProviderRates checks the DERIVE table's own
// arithmetic against the committed rate snapshot.
func TestCatalogAliasPricesMatchProviderRates(t *testing.T) {
	migration := readPricingMigration(t)
	rates := loadProviderRates(t)

	for _, row := range parseDeriveRows(t, migration) {
		rate, ok := rates[row.ProviderModel]
		if !ok {
			t.Errorf("alias %s routes to provider_model %q, which is absent from the verified provider snapshot; either the model id is wrong (a dropped tilde or a bad date suffix looks exactly like this) or the snapshot needs re-fetching",
				row.Alias, row.ProviderModel)
			continue
		}

		var snapshotUSD string
		switch row.Field {
		case "in":
			snapshotUSD = rate.In
		case "out":
			snapshotUSD = rate.Out
		case "cache_read":
			if rate.CacheRead == nil {
				t.Errorf("alias %s documents a cache_read price for %q, but the provider publishes no cache-read rate for that model", row.Alias, row.ProviderModel)
				continue
			}
			snapshotUSD = *rate.CacheRead
		}

		// Compared as rationals, not as strings: "0.3" and "0.30" are the same
		// rate, and a string comparison would report a false red on a purely
		// cosmetic difference in how one of the two files writes it.
		documented, err := parseRate(row.USD)
		if err != nil {
			t.Errorf("alias %s %s: migration's documented %v", row.Alias, row.Field, err)
			continue
		}
		snapshot, err := parseRate(snapshotUSD)
		if err != nil {
			t.Errorf("alias %s %s: snapshot's %v", row.Alias, row.Field, err)
			continue
		}
		if documented.Cmp(snapshot) != 0 {
			t.Errorf("alias %s %s: migration documents the provider rate as $%s per million, snapshot recorded $%s",
				row.Alias, row.Field, row.USD, snapshotUSD)
			continue
		}

		want := expectedCredits(snapshot)
		if want.String() != row.Credits {
			t.Errorf("alias %s %s: ceil($%s * 1.4 * 100000) = %s credits, migration claims %s",
				row.Alias, row.Field, snapshotUSD, want.String(), row.Credits)
		}
	}
}

// TestDeclaredCreditsLandOnTheirOwnAliasRow is THE money-path guard. It binds
// each DERIVE figure to the specific column of the specific alias row, so a
// figure that is right in the comment and wrong in the SQL, or right for one
// alias and applied to another, fails.
func TestDeclaredCreditsLandOnTheirOwnAliasRow(t *testing.T) {
	migration := readPricingMigration(t)
	sql := stripSQLComments(migration)
	priced := pricedAliases(t, sql)

	for _, row := range parseDeriveRows(t, migration) {
		aliasRow, ok := priced[row.Alias]
		if !ok {
			t.Errorf("DERIVE documents a price for alias %s, but the migration writes no price for it at all", row.Alias)
			continue
		}
		column := deriveField[row.Field]
		got, present := aliasRow[column]
		if !present {
			t.Errorf("alias %s: DERIVE declares %s = %s credits, but the migration never writes column %s for that alias",
				row.Alias, row.Field, row.Credits, column)
			continue
		}
		if got != row.Credits {
			t.Errorf("alias %s: DERIVE declares %s = %s credits, but the migration writes %s = %s. The comment and the SQL disagree, and the SQL is what customers are charged.",
				row.Alias, row.Field, row.Credits, column, got)
		}
	}
}

// TestEveryPricedAliasHasCompleteDerivation is the floor under parseDeriveRows.
// Without it the checked set can silently shrink: deleting the two hive-default
// DERIVE lines left every other assertion green, and hive-default is the alias
// every request naming no model lands on.
func TestEveryPricedAliasHasCompleteDerivation(t *testing.T) {
	migration := readPricingMigration(t)
	sql := stripSQLComments(migration)

	documented := map[string]map[string]bool{}
	for _, row := range parseDeriveRows(t, migration) {
		if documented[row.Alias] == nil {
			documented[row.Alias] = map[string]bool{}
		}
		documented[row.Alias][row.Field] = true
	}

	for alias, row := range pricedAliases(t, sql) {
		for _, field := range []string{"in", "out"} {
			if !documented[alias][field] {
				t.Errorf("alias %s has its %s price written by this migration (%s) but no '-- DERIVE|' row documenting where that figure came from",
					alias, field, row[deriveField[field]])
			}
		}
	}
}

// TestDerivedRouteMatchesProviderRoutes binds the DERIVE table's route and
// upstream model to the provider_routes SQL. Without it, repointing a route at
// a different model while leaving its price alone passes everything: the
// provider_model was checked against the rate snapshot but never against the
// migration, which is issue #689 and #965 in miniature.
func TestDerivedRouteMatchesProviderRoutes(t *testing.T) {
	migration := readPricingMigration(t)
	sql := stripSQLComments(migration)

	routes := map[string]map[string]string{}
	for _, r := range insertRows(sql, "public.provider_routes") {
		routes[r["route_id"]] = r
	}
	if len(routes) == 0 {
		t.Fatal("parsed no provider_routes rows; the parser and the migration have diverged")
	}

	for _, row := range parseDeriveRows(t, migration) {
		route, ok := routes[row.RouteID]
		if !ok {
			t.Errorf("alias %s is priced against route %s, which this migration never inserts", row.Alias, row.RouteID)
			continue
		}
		if route["alias_id"] != row.Alias {
			t.Errorf("route %s is priced under alias %s but provider_routes attaches it to %s", row.RouteID, row.Alias, route["alias_id"])
		}
		if route["provider_model"] != row.ProviderModel {
			t.Errorf("alias %s is priced against upstream model %q, but route %s actually calls %q. The price and the model have drifted apart, which is a silent over or undercharge.",
				row.Alias, row.ProviderModel, row.RouteID, route["provider_model"])
		}
		// provider must agree with the model's own prefix, or the config sync
		// picks the wrong api_base and API key for it.
		if wantProvider := strings.SplitN(row.ProviderModel, "/", 2)[0]; route["provider"] != wantProvider {
			t.Errorf("route %s calls %q but declares provider %q; the sync would send this model, and that provider's key, to the wrong endpoint",
				row.RouteID, row.ProviderModel, route["provider"])
		}
	}
}

// TestOneEnabledRoutePerAliasInSQL enforces the owner's one-alias-one-price rule
// against provider_routes itself rather than against the comment table. A second
// enabled route makes an alias's cost depend on which route won, which is not
// priceable at the alias level.
//
// This reads only this migration, so it cannot see routes added elsewhere. The
// database-level invariant across the whole catalog is
// TestSeededAliasHasExactlyOneEnabledRoute in catalog_pricing_integration_test.go,
// which needs ROUTING_TEST_DB_URL. Do not read this offline guard as that one.
func TestOneEnabledRoutePerAliasInSQL(t *testing.T) {
	sql := migrationSQL(t)

	enabledByAlias := map[string][]string{}
	for _, r := range insertRows(sql, "public.provider_routes") {
		if strings.EqualFold(r["health_state"], "disabled") || strings.EqualFold(r["health_state"], "eol") {
			continue
		}
		enabledByAlias[r["alias_id"]] = append(enabledByAlias[r["alias_id"]], r["route_id"])
	}
	if len(enabledByAlias) == 0 {
		t.Fatal("parsed no enabled provider_routes rows; this guard is checking nothing")
	}

	for alias, routes := range enabledByAlias {
		if len(routes) != 1 {
			t.Errorf("alias %s is given %d enabled routes by this migration (%s); the one-alias-one-price rule allows exactly one",
				alias, len(routes), strings.Join(routes, ", "))
		}
	}
}

// TestInsertedAliasesArePinnedToTheirRoute checks the policy row for aliases
// this migration creates. A repointed alias keeps the policy an earlier
// migration gave it, so it is covered by TestRetiredRoutesAreDisabledAndRepointed
// instead.
func TestInsertedAliasesArePinnedToTheirRoute(t *testing.T) {
	sql := migrationSQL(t)

	routeOf := map[string]string{}
	for _, r := range insertRows(sql, "public.provider_routes") {
		if !strings.EqualFold(r["health_state"], "disabled") {
			routeOf[r["alias_id"]] = r["route_id"]
		}
	}

	policies := map[string]string{}
	for _, p := range insertRows(sql, "public.alias_route_policies") {
		policies[p["alias_id"]] = p["fallback_order"]
	}

	for _, row := range insertRows(sql, "public.model_aliases") {
		alias := row["alias_id"]
		order, ok := policies[alias]
		if !ok {
			t.Errorf("alias %s is inserted with no alias_route_policies row", alias)
			continue
		}
		want := `["` + routeOf[alias] + `"]`
		if strings.ReplaceAll(order, " ", "") != want {
			t.Errorf("alias %s has fallback_order %s; the one-alias-one-price rule requires exactly %s", alias, order, want)
		}
	}
}

// retiredRoutes are the OpenRouter routes hive-default and hive-auto used to
// take, mapped to the Groq route each alias moves onto.
var retiredRoutes = map[string]struct{ alias, replacement string }{
	"route-openrouter-default": {"hive-default", "route-groq-default"},
	"route-openrouter-auto":    {"hive-auto", "route-groq-auto"},
}

// TestRetiredRoutesAreDisabledAndRepointed guards the move that has to happen
// as a whole for hive-default and hive-auto.
//
// The old route must be DISABLED rather than repointed in place, because
// litellmconfig's mergeParams deliberately preserves every litellm_params key
// the database does not own. The OpenRouter-specific extra_body block those two
// entries carry would otherwise stay attached to a route now pointing at Groq
// and be sent to Groq on every request to the default model, with no sync able
// to remove it. Disabling the route id makes the merge drop the stale entry.
func TestRetiredRoutesAreDisabledAndRepointed(t *testing.T) {
	sql := migrationSQL(t)

	// Per statement, not over a concatenation of all of them: a file disabling
	// route A in one statement and merely mentioning route B in another must
	// not satisfy the guard for B.
	var disablers []string
	for _, stmt := range splitStatements(sql) {
		if disableRe.MatchString(strings.TrimSpace(stmt)) {
			disablers = append(disablers, stmt)
		}
	}
	if len(disablers) == 0 {
		t.Fatal("no statement disables any provider_route; the two OpenRouter routes must be retired, not left enabled alongside their replacements")
	}

	policyUpdates := updateAssignments(sql, "public.alias_route_policies", "alias_id")

	for old, move := range retiredRoutes {
		disabled := false
		for _, stmt := range disablers {
			if strings.Contains(stmt, "'"+old+"'") {
				disabled = true
				break
			}
		}
		if !disabled {
			t.Errorf("route %s is still enabled: it must be disabled when %s moves to %s, or the alias has two enabled routes and stops being priceable",
				old, move.alias, move.replacement)
		}

		order := strings.ReplaceAll(policyUpdates[move.alias]["fallback_order"], " ", "")
		if want := `["` + move.replacement + `"]`; order != want {
			t.Errorf("%s needs its alias_route_policies fallback_order updated to %s, found %q; otherwise the policy keeps naming the retired route %s",
				move.alias, want, order, old)
		}

		// The stale entry must not be resurrected by an in-place repoint.
		// Accept both the `= 'x'` and the `IN ('x', ...)` forms, since this
		// migration's own retirement statement uses IN and a repoint written
		// that way would otherwise walk past the check.
		for _, stmt := range splitStatements(sql) {
			lower := strings.ToLower(strings.TrimSpace(stmt))
			if !strings.HasPrefix(lower, "update public.provider_routes") {
				continue
			}
			if !strings.Contains(lower, "provider_model") {
				continue
			}
			if strings.Contains(stmt, "'"+old+"'") {
				t.Errorf("route %s is repointed in place; it must be disabled instead, or its OpenRouter extra_body block survives the config sync and is sent to Groq", old)
			}
		}
	}
}

// soleCarrierFlags are capability flags held by exactly ONE route in the whole
// catalog before this migration, route-openrouter-auto, granted by
// 20260414_01_provider_capabilities_media_columns.sql and never granted since.
var soleCarrierFlags = []string{
	"supports_batch",
	"supports_image_generation",
	"supports_image_edit",
}

// TestDisablingASoleCapabilityCarrierHandsItsFlagsOn is a regression guard for a
// real defect an earlier revision of this migration shipped.
//
// Disabling route-openrouter-auto removes the only route carrying these three
// flags. SelectRoute skips disabled candidates and then hard filters on each
// flag, and both batchstore/submitter.go and local_executor_adapters.go send
// NeedBatch for EVERY batch, so /v1/batches, /v1/images/generations and
// /v1/images/edits each find zero eligible routes for every alias in the system.
// It fails closed, so nothing reports it.
func TestDisablingASoleCapabilityCarrierHandsItsFlagsOn(t *testing.T) {
	sql := migrationSQL(t)

	stillDisabled := false
	for _, stmt := range splitStatements(sql) {
		// disableRe, not a literal substring: `health_state='disabled'`, or the
		// assignment broken across lines, would otherwise leave stillDisabled
		// false and send this guard to the t.Skip below, while the migration went
		// on disabling the route and stripping its three flags unobserved.
		if disableRe.MatchString(strings.TrimSpace(stmt)) && strings.Contains(stmt, "'route-openrouter-auto'") {
			stillDisabled = true
			break
		}
	}
	if !stillDisabled {
		t.Skip("route-openrouter-auto is not disabled by this migration, so its capabilities are not at risk")
	}

	caps := insertRows(sql, "public.provider_capabilities")
	if len(caps) == 0 {
		t.Fatal("route-openrouter-auto is disabled but this migration inserts no provider_capabilities rows to hand its flags to")
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
			t.Errorf("this migration disables route-openrouter-auto, the only route in the catalog carrying %s, and grants that flag to no replacement route. Every endpoint gated on it would find zero eligible routes for every alias.", flag)
		}
	}
}

// TestRepointedAliasesKeepTheirCapabilities is the sibling of the guard above
// for the per-alias flags. hive-default and hive-auto both had
// supports_reasoning true on their OpenRouter routes. Because every alias here
// is pinned to exactly ONE route, a narrower replacement does not quietly
// withhold a feature: matchesRequestedCapabilities drops the only candidate and
// SelectRoute returns ErrRouteNotEligible, which writeRoutingError maps to 422.
// So a chat completion carrying reasoning_effort would start failing outright.
func TestRepointedAliasesKeepTheirCapabilities(t *testing.T) {
	sql := migrationSQL(t)

	caps := map[string]map[string]string{}
	for _, row := range insertRows(sql, "public.provider_capabilities") {
		caps[row["route_id"]] = row
	}

	for _, move := range retiredRoutes {
		row, ok := caps[move.replacement]
		if !ok {
			t.Errorf("replacement route %s has no provider_capabilities row, so it takes the column defaults of false for everything", move.replacement)
			continue
		}
		if !strings.EqualFold(row["supports_reasoning"], "true") {
			t.Errorf("route %s must keep supports_reasoning = true: %s is pinned to this single route, its previous OpenRouter route had the flag, and gpt-oss does expose reasoning effort. With one candidate an under-claim is not a withheld feature, it is a 422 on every request carrying reasoning_effort.",
				move.replacement, move.alias)
		}
	}
}

// TestHiveFastStaysInvocableAfterDeprecation guards the back-compat promise.
// hive-fast is the model id stored in existing Open WebUI conversations and in
// live API clients, so the restructure must not remove it, must not stop it
// resolving, and must not reprice it.
//
// Every check here is statement scoped. An earlier version used `.*?` under
// `(?s)`, which let the pieces come from three different statements: it could
// report hive-fast marked hidden when a DIFFERENT alias had been hidden, which
// is a false green on a back-compat guard.
func TestHiveFastStaysInvocableAfterDeprecation(t *testing.T) {
	sql := migrationSQL(t)

	for _, stmt := range splitStatements(sql) {
		lower := strings.ToLower(strings.TrimSpace(stmt))
		if strings.HasPrefix(lower, "delete from public.model_aliases") || strings.HasPrefix(lower, "delete from model_aliases") {
			t.Error("the restructure must not DELETE from model_aliases: hive-fast is stored per-chat in existing Open WebUI conversations and would 404 for every one of them")
		}
	}

	updates := updateAssignments(sql, "public.model_aliases", "alias_id")
	hiveFast, ok := updates["hive-fast"]
	if !ok {
		t.Fatal("hive-fast must be marked with lifecycle = 'hidden'; that is the deprecation marker this restructure promised, and no UPDATE targets it")
	}

	if hiveFast["lifecycle"] != "hidden" {
		t.Errorf("hive-fast's lifecycle is set to %q; the deprecation marker must be 'hidden'. model_aliases' CHECK constraint permits only stable, preview and hidden, so a literal 'deprecated' would abort the migration on apply.", hiveFast["lifecycle"])
	}
	if v, set := hiveFast["visibility"]; set && (v == "internal" || v == "restricted") {
		t.Errorf("hive-fast must keep visibility 'public', found %q: AliasVisibleToTenant fails closed on anything but public and preview, which would block invocation, not merely hide the alias from the picker", v)
	}
	for _, priceCol := range []string{"input_price_credits", "output_price_credits"} {
		if v, set := hiveFast[priceCol]; set {
			t.Errorf("hive-fast must not be repriced by this migration, but %s is set to %s; it has to keep charging exactly what it charged before", priceCol, v)
		}
	}
}

// TestNewAliasesReachDefaultTierKeys is the inert-change guard. An alias can be
// inserted, priced, routed and wired into LiteLLM and still be invisible to
// every customer, because api_key_policies.allowed_group_names defaults to
// ["default"] and a key never sees an alias outside its groups. That gap has
// been patched by hand twice already, by 20260717_01 and 20260717_02.
func TestNewAliasesReachDefaultTierKeys(t *testing.T) {
	sql := migrationSQL(t)

	inDefault := map[string]bool{}
	for _, row := range insertRows(sql, "public.model_policy_group_members") {
		if row["group_name"] == "default" {
			inDefault[row["alias_id"]] = true
		}
	}

	for _, row := range insertRows(sql, "public.model_aliases") {
		if alias := row["alias_id"]; !inDefault[alias] {
			t.Errorf("alias %s is never added to the 'default' model policy group, so no default-tier API key can call it; the alias would ship inert", alias)
		}
	}
}
