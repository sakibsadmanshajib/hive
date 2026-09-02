package routing

import (
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Guards for supabase/migrations/20260902_02_retire_alias_margin_factor.sql,
// the data half of issue #1692: every authored price is re-derived at the true
// provider list rate, with the 1.4 margin multiplier removed (D-064). The Go
// half of the same change removes the runtime factor from the two settlement
// paths, and its own guards live beside them.
//
// The pattern is the one catalog_alias_pricing_test.go established for
// 20260822_02 and states the reason for at length: digits somewhere in a file
// is not an assertion, a value in a named column of a named row is. That file
// is pinned to its own migration by filename and inherits nothing to a later
// one, which is why this is a separate file rather than an edit to it.

const marginRetirementMigrationRelPath = "supabase/migrations/20260902_02_retire_alias_margin_factor.sql"

// creditsPerUSDPostRescale is the peg every figure in the guarded migration is
// derived at (D-046). Deliberately a literal rather than an import of
// payments.CreditsPerUSD: this file checks that a MIGRATION's arithmetic is
// right, and taking the multiplier from the same package the migration is
// supposed to agree with would let both move together unnoticed.
const creditsPerUSDPostRescale = 1_000_000_000

// deepSeekCacheReadDivisor is the published ratio between DeepSeek's input rate
// and its cache-read rate, established by
// 20260825_02_deepseek_cache_read_price_correction.sql. The provider snapshot
// carries a cache-read rate directly for these models, so this is a
// cross-check rather than the source.
const deepSeekCacheReadDivisor = 30

// marginRetirementField maps a DERIVE field name to the column it must land in.
// It carries cache_write, which the 20260822_02 table never needed.
var marginRetirementField = map[string]string{
	"in":          "input_price_credits",
	"out":         "output_price_credits",
	"cache_read":  "cache_read_price_credits",
	"cache_write": "cache_write_price_credits",
}

func marginRetirementMigration(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, marginRetirementMigrationRelPath)
}

// parseMarginRetirementDeriveRows reads the migration's own DERIVE table.
//
// Every malformed shape is fatal rather than skipped. A parser that quietly
// returns nothing turns every assertion below into a loop over an empty slice,
// which reports green while checking nothing.
func parseMarginRetirementDeriveRows(t *testing.T, migration string) []deriveRow {
	t.Helper()

	matches := deriveLineRe.FindAllStringSubmatch(migration, -1)
	if len(matches) == 0 {
		t.Fatalf("%s declares no '-- DERIVE|' rows; every price in this repo must show its derivation", marginRetirementMigrationRelPath)
	}

	var rows []deriveRow
	for _, m := range matches {
		fields := strings.Split(m[1], "|")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if len(fields) > 0 && strings.EqualFold(fields[0], "alias_id") {
			continue
		}
		if len(fields) != 6 {
			t.Fatalf("malformed DERIVE row %q: want 6 pipe-separated fields (alias|route|provider_model|field|usd|credits), got %d", m[1], len(fields))
		}
		if _, ok := marginRetirementField[fields[3]]; !ok {
			t.Fatalf("DERIVE row %q has unknown field %q", m[1], fields[3])
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

// creditsAtThePegWithNoMargin computes ceil(usd * CREDITS_PER_USD) exactly. It
// is the whole formula: there is no margin term, and its absence is the point
// of the change this file guards.
func creditsAtThePegWithNoMargin(rate *big.Rat) *big.Int {
	product := new(big.Rat).Mul(rate, new(big.Rat).SetInt64(creditsPerUSDPostRescale))
	quotient, remainder := new(big.Int).QuoRem(product.Num(), product.Denom(), new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

// TestMarginRetirementPricesCarryNoMarginFactor is the assertion issue #1692
// asks for, on the data side: every documented figure is the list rate at the
// peg, and nothing else.
//
// The old 1.4 is also checked for explicitly rather than only implied, because
// the failure mode worth naming is a reprice that halves the removal (writing
// 1.2, say) and would otherwise fail with an unexplained number.
func TestMarginRetirementPricesCarryNoMarginFactor(t *testing.T) {
	for _, row := range parseMarginRetirementDeriveRows(t, marginRetirementMigration(t)) {
		rate, err := parseRate(row.USD)
		if err != nil {
			t.Errorf("alias %s %s: %v", row.Alias, row.Field, err)
			continue
		}

		want := creditsAtThePegWithNoMargin(rate)
		got, ok := new(big.Int).SetString(row.Credits, 10)
		if !ok {
			t.Errorf("alias %s %s: credits %q is not an integer", row.Alias, row.Field, row.Credits)
			continue
		}
		if got.Cmp(want) != 0 {
			t.Errorf("alias %s %s: ceil($%s x %d) = %s credits, migration claims %s",
				row.Alias, row.Field, row.USD, creditsPerUSDPostRescale, want, got)
			continue
		}

		// And the retired factor is really gone: the figure must not be the
		// list rate times 1.4, which is what every one of these rows said
		// before this migration.
		withOldMargin := creditsAtThePegWithNoMargin(new(big.Rat).Mul(rate, big.NewRat(7, 5)))
		if got.Cmp(withOldMargin) == 0 {
			t.Errorf("alias %s %s still carries the retired 1.4 margin (%s credits); D-064 removed it",
				row.Alias, row.Field, got)
		}
	}
}

// TestMarginRetirementRatesMatchTheProviderSnapshot checks that the rates the
// migration re-derives from are the SAME rates the catalog was priced against,
// not new ones fetched along the way. A rate change folded into a margin change
// is invisible afterwards: both move a price, and nothing says which did.
func TestMarginRetirementRatesMatchTheProviderSnapshot(t *testing.T) {
	rates := loadProviderRates(t)

	for _, row := range parseMarginRetirementDeriveRows(t, marginRetirementMigration(t)) {
		snapshot, ok := rates[row.ProviderModel]
		if !ok {
			t.Errorf("alias %s routes to provider_model %q, which is absent from the verified provider snapshot", row.Alias, row.ProviderModel)
			continue
		}

		var want string
		switch row.Field {
		case "in", "cache_write":
			// Cache WRITE is priced at the input rate
			// (20260825_01_deepseek_cache_write_price.sql), and neither
			// DeepSeek model publishes a cache-write rate of its own.
			want = snapshot.In
		case "out":
			want = snapshot.Out
		case "cache_read":
			// NOT the snapshot's own cache-read field, deliberately, and this
			// is the one place this file departs from it.
			//
			// 20260825_02_deepseek_cache_read_price_correction.sql establishes
			// that DeepSeek prices a cache read at one thirtieth of its input
			// rate, and that the snapshot's flash entry (0.01278, which is 0.2
			// times the input rate) is the RATE ERROR that migration exists to
			// correct: it had been overcharging cache hits sixfold. Checking
			// against it here would demand that this migration re-import the
			// corrected-away figure. The pro entry, 0.0374, does equal the
			// input rate over thirty and is asserted as such below, which is
			// what keeps the ratio itself sourced rather than asserted.
			want = new(big.Rat).Quo(
				wantRateOrZero(t, snapshot.In),
				new(big.Rat).SetInt64(deepSeekCacheReadDivisor),
			).FloatString(8)
		}

		got, err := parseRate(row.USD)
		if err != nil {
			t.Errorf("alias %s %s: %v", row.Alias, row.Field, err)
			continue
		}
		wantRate, err := parseRate(want)
		if err != nil {
			t.Errorf("snapshot rate for %s (%s): %v", row.ProviderModel, row.Field, err)
			continue
		}
		if got.Cmp(wantRate) != 0 {
			t.Errorf("alias %s %s: migration prices from $%s per million, snapshot says $%s. The rate must not move in a margin change.",
				row.Alias, row.Field, row.USD, want)
		}

		// deepseek-v4-pro is the one alias whose snapshot cache-read entry is
		// the correct one, so it anchors the ratio the flash alias is priced by
		// rather than leaving that ratio asserted from a migration comment.
		if row.Field == "cache_read" && row.ProviderModel == "openrouter/deepseek/deepseek-v4-pro-0813" {
			if snapshot.CacheRead == nil {
				t.Errorf("the provider snapshot has no cache-read rate for %q; it is what anchors the one-thirtieth ratio", row.ProviderModel)
				continue
			}
			anchor, err := parseRate(*snapshot.CacheRead)
			if err != nil {
				t.Errorf("snapshot cache-read rate for %q: %v", row.ProviderModel, err)
				continue
			}
			derived := new(big.Rat).Quo(wantRateOrZero(t, snapshot.In), new(big.Rat).SetInt64(deepSeekCacheReadDivisor))
			if anchor.Cmp(derived) != 0 {
				t.Errorf("the snapshot's cache-read rate for %q ($%s) is no longer the input rate over %d ($%s); the ratio the flash alias is priced by has lost its source",
					row.ProviderModel, *snapshot.CacheRead, deepSeekCacheReadDivisor, derived.FloatString(8))
			}
		}
	}
}

func wantRateOrZero(t *testing.T, usd string) *big.Rat {
	t.Helper()
	rate, err := parseRate(usd)
	if err != nil {
		t.Fatalf("snapshot input rate %q: %v", usd, err)
	}
	return rate
}

// TestMarginRetirementFiguresLandOnTheirOwnAliasRow binds each documented
// figure to the specific column of the specific alias the SQL writes, which is
// the assertion that catches a correct number written onto the wrong row. Three
// real mispricings passed the presence-of-digits version of this guard on
// 20260822_02.
func TestMarginRetirementFiguresLandOnTheirOwnAliasRow(t *testing.T) {
	sql := stripSQLComments(marginRetirementMigration(t))
	written := updateAssignments(sql, "public.model_aliases", "alias_id")
	if len(written) == 0 {
		t.Fatal("parsed no model_aliases updates out of the migration; the parser and the file have diverged and every guard here is checking nothing")
	}

	for _, row := range parseMarginRetirementDeriveRows(t, marginRetirementMigration(t)) {
		column := marginRetirementField[row.Field]
		assigns, ok := written[row.Alias]
		if !ok {
			t.Errorf("DERIVE documents a price for alias %s, but the migration writes no price for it at all", row.Alias)
			continue
		}
		value, ok := assigns[column]
		if !ok {
			t.Errorf("alias %s: DERIVE declares %s = %s credits, but the migration never writes column %s for that alias",
				row.Alias, row.Field, row.Credits, column)
			continue
		}
		if value != row.Credits {
			t.Errorf("alias %s: DERIVE declares %s = %s credits, but the migration writes %s = %s. The comment and the SQL disagree, and the SQL is what customers are charged.",
				row.Alias, row.Field, row.Credits, column, value)
		}
	}
}

// TestMarginRetirementCoversEveryRepricedAlias is the other direction: an alias
// this migration reprices with no DERIVE row behind it. Without it, a price
// could be changed with no derivation and every assertion above would still
// pass, because they all iterate the DERIVE table.
//
// The two non-token aliases are named exceptions rather than silent ones. Their
// rates are not in the text-model provider snapshot, so they are documented in
// the migration header instead and pinned by
// TestMarginRetirementRepricesTheNonTokenAliases below.
func TestMarginRetirementCoversEveryRepricedAlias(t *testing.T) {
	documentedOutsideDerive := map[string]bool{"hive-tts": true, "hive-stt": true}

	derived := map[string]map[string]bool{}
	for _, row := range parseMarginRetirementDeriveRows(t, marginRetirementMigration(t)) {
		if derived[row.Alias] == nil {
			derived[row.Alias] = map[string]bool{}
		}
		derived[row.Alias][marginRetirementField[row.Field]] = true
	}

	sql := stripSQLComments(marginRetirementMigration(t))
	for alias, assigns := range updateAssignments(sql, "public.model_aliases", "alias_id") {
		for column := range assigns {
			if !strings.HasSuffix(column, "_price_credits") {
				continue
			}
			if documentedOutsideDerive[alias] {
				continue
			}
			if !derived[alias][column] {
				t.Errorf("alias %s has its %s written by this migration but no '-- DERIVE|' row documenting where that figure came from", alias, column)
			}
		}
	}
}

// TestMarginRetirementRepricesTheNonTokenAliases pins the two figures the
// DERIVE table cannot carry, recomputed here from the published rates the
// migration header names rather than copied from the SQL.
//
//	hive-tts  22.00 USD per million characters       -> 22,000,000,000
//	hive-stt  0.111 USD per hour, per million seconds -> ceil(30,833,333,333.33)
func TestMarginRetirementRepricesTheNonTokenAliases(t *testing.T) {
	sql := stripSQLComments(marginRetirementMigration(t))
	written := updateAssignments(sql, "public.model_aliases", "alias_id")

	ttsRate, err := parseRate("22.00")
	if err != nil {
		t.Fatalf("tts rate: %v", err)
	}
	// 0.111 USD per hour is 0.111/3600 per second, so 0.111/3600 x 1e6 per
	// million seconds. Kept as an exact rational: the decimal expansion
	// repeats, and the whole point of the ceiling below is that it does.
	sttRate := new(big.Rat).Quo(
		new(big.Rat).Mul(big.NewRat(111, 1000), new(big.Rat).SetInt64(1_000_000)),
		new(big.Rat).SetInt64(3600),
	)

	for _, tc := range []struct {
		alias string
		rate  *big.Rat
	}{
		{"hive-tts", ttsRate},
		{"hive-stt", sttRate},
	} {
		assigns, ok := written[tc.alias]
		if !ok {
			t.Errorf("%s is not repriced by this migration; it carried the same 1.4 factor as the token aliases", tc.alias)
			continue
		}
		value, ok := assigns["output_price_credits"]
		if !ok {
			t.Errorf("%s: the migration writes no output_price_credits", tc.alias)
			continue
		}
		want := creditsAtThePegWithNoMargin(tc.rate)
		if value != want.String() {
			t.Errorf("%s: output_price_credits = %s, want %s (the list rate at the peg, no margin)", tc.alias, value, want)
		}
	}
}

// TestMarginRetirementLeavesTheFreeAliasesAlone is the negative half of the
// sweep. hive-free and hive-free-tools are owner-set service prices on
// zero-cost upstreams, not cost-derived figures, and dividing them by 1.4 would
// both invent a price nobody set and break the halving relation
// free_alias_pricing_test.go checks.
func TestMarginRetirementLeavesTheFreeAliasesAlone(t *testing.T) {
	sql := stripSQLComments(marginRetirementMigration(t))
	written := updateAssignments(sql, "public.model_aliases", "alias_id")

	for _, alias := range []string{"hive-free", "hive-free-tools", "hive-embedding-default", "hive-web-search", "hive-web-fetch"} {
		if assigns, ok := written[alias]; ok {
			for column := range assigns {
				if strings.HasSuffix(column, "_price_credits") {
					t.Errorf("%s: this migration writes %s, but that price is not margin-derived and is outside the scope of removing the margin", alias, column)
				}
			}
		}
	}
}

// TestMarginRetirementMovesTheFXFeeDefault pins the second half of the deploy:
// the fx_snapshots default has to move with payments.FXFeeRate, or a row
// written outside the Go path would carry a fee that was never applied to it.
func TestMarginRetirementMovesTheFXFeeDefault(t *testing.T) {
	sql := stripSQLComments(marginRetirementMigration(t))
	if !fxFeeDefaultRe.MatchString(sql) {
		t.Errorf("%s does not set the fx_snapshots.fee_rate default to '0.025'; the stored fee and the applied fee would disagree", marginRetirementMigrationRelPath)
	}
}

var fxFeeDefaultRe = regexp.MustCompile(`(?is)alter\s+table\s+public\.fx_snapshots\s+alter\s+column\s+fee_rate\s+set\s+default\s+'0\.025'`)

// TestMarginRetirementPricesAreAllSmaller states the direction of the change in
// one assertion: every figure this migration writes is strictly below what the
// catalog held before it, because removing a multiplier greater than one can
// only reduce a price. A reprice that raised anything is a mistake whatever its
// derivation says.
func TestMarginRetirementPricesAreAllSmaller(t *testing.T) {
	previous := map[string]map[string]int64{
		"hive-small":        {"input_price_credits": 105000000, "output_price_credits": 420000000},
		"hive-medium":       {"input_price_credits": 210000000, "output_price_credits": 840000000},
		"hive-fast":         {"input_price_credits": 105000000, "output_price_credits": 420000000},
		"hive-default":      {"input_price_credits": 89460000, "output_price_credits": 178920000, "cache_read_price_credits": 2982000},
		"deepseek-v4-flash": {"input_price_credits": 89460000, "output_price_credits": 178920000, "cache_read_price_credits": 2982000, "cache_write_price_credits": 89460000},
		"deepseek-v4-pro":   {"input_price_credits": 1570800000, "output_price_credits": 4712400000, "cache_read_price_credits": 52360000, "cache_write_price_credits": 1570800000},
		"hive-tts":          {"output_price_credits": 30800000000},
		"hive-stt":          {"output_price_credits": 43166670000},
	}

	sql := stripSQLComments(marginRetirementMigration(t))
	written := updateAssignments(sql, "public.model_aliases", "alias_id")

	for alias, columns := range previous {
		assigns, ok := written[alias]
		if !ok {
			t.Errorf("%s carried the 1.4 margin and is not repriced by this migration", alias)
			continue
		}
		for column, before := range columns {
			value, ok := assigns[column]
			if !ok {
				t.Errorf("%s: %s carried the margin and is not rewritten", alias, column)
				continue
			}
			after, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				t.Errorf("%s %s = %q, not an integer", alias, column, value)
				continue
			}
			if after >= before {
				t.Errorf("%s %s went from %d to %d; removing the 1.4 margin can only lower a price", alias, column, before, after)
			}
		}
	}
}
