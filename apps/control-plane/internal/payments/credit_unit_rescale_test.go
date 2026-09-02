package payments

import (
	"math/big"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The credit unit rescale (owner directive, 2026-08-23): 1 USD went from
// 100,000 Hive Credits to 1,000,000,000, a factor of exactly 10,000. These
// guards hold three properties:
//
//  1. The constants themselves moved together (no straggler).
//  2. Purchase validation speaks whole cents at the NEW unit.
//  3. The data migration rescales EVERY stored credit column by the same
//     factor, so a real dollar billed before the deploy bills the same real
//     dollar after it.
//
// Property 3 matters more than it looks: a catalog price or ledger delta left
// unscaled does not fail any compiler, it silently reprices the product by
// four orders of magnitude. Parsing the migration here is the same
// positional-guard pattern catalog_alias_pricing_test.go uses on the routing
// side: digits somewhere in a file is not an assertion; a named column inside
// a multiplication by the factor is.
const rescaleMigrationRelPath = "../../../../supabase/migrations/20260823_40_credit_unit_rescale_billion.sql"

const (
	oldCreditsPerUSD = int64(100_000)
	rescaleFactor    = int64(10_000) // 1e9 / 1e5
)

func TestCreditUnitConstantsMovedTogether(t *testing.T) {
	if CreditsPerUSD != 1_000_000_000 {
		t.Fatalf("CreditsPerUSD = %d, want 1000000000", CreditsPerUSD)
	}
	if got := CreditsPerUSD / oldCreditsPerUSD; got != rescaleFactor {
		t.Fatalf("rescale factor = %d, want %d", got, rescaleFactor)
	}
	if CreditIncrement != CreditsPerUSD/100 {
		t.Fatalf("CreditIncrement = %d, want one cent (%d)", CreditIncrement, CreditsPerUSD/100)
	}
	// What MinPurchaseCredits must CLEAR is deliberately not asserted here any
	// more. It used to be pinned to one cent step, which is a bare number
	// standing in for a property nobody had checked: the floor exists to clear
	// the chat authorization hold, and pinned to one cent it was one tenth of
	// one hold (issue #1450). That relationship is with constants in another
	// module, so it is asserted in purchase_floor_test.go where they can be
	// read. What this file still owes the constant, and asserts below, is that
	// it speaks the current unit in whole cent steps.
	if MinPurchaseCredits%CreditIncrement != 0 {
		t.Fatalf("MinPurchaseCredits = %d, want a whole one-cent step of %d", MinPurchaseCredits, CreditIncrement)
	}
	if MaxPurchaseCreditsStripe != 100*CreditsPerUSD {
		t.Fatalf("MaxPurchaseCreditsStripe = %d, want %d (100 USD)", MaxPurchaseCreditsStripe, 100*CreditsPerUSD)
	}
}

// TestValidatePurchaseAmountSpeaksWholeCents pins the purchase granularity at
// the new unit: a whole one-cent step passes, anything finer is refused.
func TestValidatePurchaseAmountSpeaksWholeCents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		credits int64
		ok      bool
	}{
		// Granularity is the property under test, so every case sits above
		// the purchase floor except where noted: a case below the floor
		// would be refused for a reason this test is not about, and would
		// go on reading green while granularity checking rotted away.
		{"one cent step above the floor", MinPurchaseCredits + CreditIncrement, true},
		{"one dollar", CreditsPerUSD, true},
		{"stripe max", MaxPurchaseCreditsStripe, true},
		{"half a cent above the floor", MinPurchaseCredits + CreditIncrement/2, false},
		// Both pre-rescale figures are lifted above the floor for the same
		// reason as the two cases above: at their bare values they now fail the
		// floor, and would go on passing with the granularity check removed.
		{"pre-rescale one-cent step, above the floor", MinPurchaseCredits + 1_000, false},
		{"pre-rescale one dollar, above the floor", MinPurchaseCredits + oldCreditsPerUSD, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePurchaseAmount(tc.credits, RailStripe)
			if tc.ok && err != nil {
				t.Fatalf("want accept, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want reject, got nil")
			}
		})
	}
}

// The tier assertions that used to sit here are gone rather than updated.
// They pinned {1, 5, 10, 50, 100} cents, which matched the constants exactly
// and so proved only that nobody had retyped them, which is how issue #1450
// shipped two tiers that could not buy one chat message. Every property a tier
// must now hold, including the whole-cent unit this file cares about, is
// asserted against the purchase floor in purchase_floor_test.go, and a second
// copy here would be a strict subset that cannot go red on its own.

// --- The data migration -----------------------------------------------------

type rescaleStmt struct {
	table   string
	columns []string
}

// everyRescaledColumn is the complete inventory of stored credit quantities,
// from the migration header. A column missing here but present in the schema
// means the migration under-covers and this test fails.
var everyRescaledColumn = []rescaleStmt{
	{"model_aliases", []string{
		"input_price_credits", "output_price_credits",
		"cache_read_price_credits", "cache_write_price_credits"}},
	{"model_aliases", []string{"reservation_estimate_credits"}},
	{"credit_ledger_entries", []string{"credits_delta"}},
	{"credit_reservations", []string{"reserved_credits", "consumed_credits", "released_credits"}},
	{"credit_reservation_events", []string{"credits_delta"}},
	{"payment_intents", []string{"credits"}},
	{"payment_invoices", []string{"credits"}},
	{"api_key_policies", []string{"budget_limit_credits"}},
	{"account_budget_thresholds", []string{"threshold_credits"}},
	{"api_key_usage_rollups", []string{"consumed_credits"}},
	{"api_key_budget_windows", []string{"consumed_credits", "reserved_credits"}},
	{"batches", []string{"estimated_credits", "actual_credits"}},
	{"batch_lines", []string{"consumed_credits"}},
	{"llm_traces", []string{"cost_credits"}},
}

// stmtForTable returns the SQL text from the chosen `UPDATE public.<table>`
// up to the NEXT `UPDATE public.` of ANY table (or EOF), so assertions stay
// scoped to their own statement instead of passing because some later
// statement multiplied the right column name.
func stmtForTable(t *testing.T, raw, table string, occurrence int) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)UPDATE public\.` + regexp.QuoteMeta(table) + `\b`)
	locs := re.FindAllStringIndex(raw, -1)
	if occurrence >= len(locs) {
		t.Fatalf("migration has no UPDATE public.%s (occurrence %d)", table, occurrence)
	}
	start := locs[occurrence][0]
	end := len(raw)
	nextAny := regexp.MustCompile(`(?s)UPDATE public\.`).FindStringIndex(raw[start+len("UPDATE public."):])
	if nextAny != nil {
		end = start + len("UPDATE public.") + nextAny[0]
	}
	return raw[start:end]
}

func loadRescaleMigration(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(rescaleMigrationRelPath)
	if err != nil {
		t.Fatalf("cannot read %s, which these guards depend on: %v", rescaleMigrationRelPath, err)
	}
	return string(raw)
}

// TestRescaleMigrationMultipliesEveryCreditColumn walks the inventory above
// and requires each named column to appear in its own table's UPDATE as
// `<column> = <column> * factor`.
func TestRescaleMigrationMultipliesEveryCreditColumn(t *testing.T) {
	raw := loadRescaleMigration(t)

	occurrence := map[string]int{}
	for _, stmt := range everyRescaledColumn {
		n := occurrence[stmt.table]
		occurrence[stmt.table] = n + 1
		body := stmtForTable(t, raw, stmt.table, n)
		for _, col := range stmt.columns {
			re := regexp.MustCompile(regexp.QuoteMeta(col) + `\s*=\s*` + regexp.QuoteMeta(col) + `\s*\*\s*factor`)
			if !re.MatchString(body) {
				t.Errorf("UPDATE public.%s does not multiply %s by the factor; an unscaled column silently reprices the product by 10000x",
					stmt.table, col)
			}
		}
	}
}

// TestRescaleMigrationGuardAndMarker checks the replay guard, the audit flag
// and the boundary marker that the header promises.
func TestRescaleMigrationGuardAndMarker(t *testing.T) {
	raw := loadRescaleMigration(t)

	for _, fragment := range []struct{ what, needle string }{
		{"marker table", "CREATE TABLE IF NOT EXISTS public.credit_unit_rescale"},
		{"marker single-row PK", "PRIMARY KEY CHECK (id = 1)"},
		{"RLS enabled", "ALTER TABLE public.credit_unit_rescale ENABLE ROW LEVEL SECURITY"},
		{"RLS forced", "ALTER TABLE public.credit_unit_rescale FORCE ROW LEVEL SECURITY"},
		{"anon revoked (role-guarded)", "revoke all on public.credit_unit_rescale from anon"},
		{"replay guard", "IF EXISTS (SELECT 1 FROM public.credit_unit_rescale)"},
		{"boundary row insert with clock_timestamp", "INSERT INTO public.credit_unit_rescale (id, applied_at) VALUES (1, clock_timestamp())"},
		{"ledger audit flag", `'credit_unit', 'legacy-1usd-100k-credits'`},
		{"event audit flag", `'credit_unit', 'legacy-1usd-100k-credits'`},
	} {
		if !strings.Contains(raw, fragment.needle) {
			t.Errorf("migration missing the %s (expected %q)", fragment.what, fragment.needle)
		}
	}

	// The guard must fire BEFORE any data UPDATE: with the marker armed first
	// a replay would be a no-op, which is the idempotency proof.
	guardIdx := strings.Index(raw, "IF EXISTS (SELECT 1 FROM public.credit_unit_rescale)")
	firstUpdate := strings.Index(raw, "UPDATE public.")
	if guardIdx < 0 || firstUpdate < 0 || guardIdx > firstUpdate {
		t.Fatalf("replay guard must precede every UPDATE (guard=%d, first update=%d)", guardIdx, firstUpdate)
	}
}

// TestRescaledChargeKeepsRealUSDParity is the money proof behind the whole
// change: the SAME physical request priced against the OLD unit with the OLD
// catalog price and against the NEW unit with the NEW catalog price implies
// the identical USD amount, within integer rounding. hive-fast-era rates
// (0.05 in / 0.08 out USD per million, margin included), 40k prompt +
// 12k completion tokens.
func TestRescaledChargeKeepsRealUSDParity(t *testing.T) {
	type side struct {
		label     string
		in, out   int64 // credits per million tokens
		promptTok int64
		compleTok int64
	}
	sides := []side{
		{label: "old unit", in: 7_000, out: 11_200, promptTok: 40_000, compleTok: 12_000},
		{label: "new unit", in: 70_000_000, out: 112_000_000, promptTok: 40_000, compleTok: 12_000},
	}

	charge := func(s side) int64 {
		total := new(big.Int).Add(
			new(big.Int).Mul(big.NewInt(s.in), big.NewInt(s.promptTok)),
			new(big.Int).Mul(big.NewInt(s.out), big.NewInt(s.compleTok)),
		)
		// round half up over 1e6, mirroring metering.ChargeCredits
		q, r := new(big.Int).QuoRem(total, big.NewInt(1_000_000), new(big.Int))
		if new(big.Int).Mul(r, big.NewInt(2)).Cmp(big.NewInt(1_000_000)) >= 0 {
			q = new(big.Int).Add(q, big.NewInt(1))
		}
		return q.Int64()
	}

	usd := func(credits int64, perUSD int64) *big.Rat {
		return new(big.Rat).SetFrac(big.NewInt(credits), big.NewInt(perUSD))
	}

	// The exact real-money cost of this request shape: rate x margin as an
	// exact rational built from integers only (a float64 seed would make the
	// Cmp below flaky at the twelfth digit). rate*margin =
	// (0.05*40000 + 0.08*12000)/1e6 * 14/10 = 2960*14 / 1e7.
	trueUSD := new(big.Rat).SetFrac(big.NewInt(41_440), big.NewInt(10_000_000))

	oldCharge := charge(sides[0])
	newCharge := charge(sides[1])

	// The new unit must imply the TRUE cost exactly (its granularity is fine
	// enough that this request leaves no rounding residue).
	if got := usd(newCharge, CreditsPerUSD); got.Cmp(trueUSD) != 0 {
		t.Fatalf("new-unit implied USD = %s, want exact %s", got.FloatString(12), trueUSD.FloatString(12))
	}

	// The old unit could only imply the truth within one OLD credit of it;
	// the rescaled charge must land inside that same band, i.e. the customer
	// pays the same real money before and after the cutover.
	diff := new(big.Rat).Sub(usd(oldCharge, oldCreditsPerUSD), usd(newCharge, CreditsPerUSD))
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}
	if diff.Cmp(big.NewRat(1, oldCreditsPerUSD)) > 0 {
		t.Fatalf("implied USD diverged by more than one pre-rescale credit: %s", diff.FloatString(12))
	}
}

// =============================================================================
// Issue #1704: the detector this migration documented, and the remedy next to
// it, are the thing under test here. Nothing was wrong with the DATA; the
// danger was the instruction. These two guards keep the correction in place.
// =============================================================================

const stragglerMigrationRelPath = "../../../../supabase/migrations/20260902_02_credit_unit_straggler_detector.sql"

// TestRescaleMigrationDocumentsNoBlanketRemedy asserts the header no longer
// carries a query that reads a missing stamp as an old unit, nor an
// instruction to multiply its matches by the rescale factor. On the live box
// that pair pointed at seven correctly scaled grants worth 221 billion
// credits, and following it would have inflated them ten thousand fold.
func TestRescaleMigrationDocumentsNoBlanketRemedy(t *testing.T) {
	raw := loadRescaleMigration(t)

	for _, banned := range []struct{ what, needle string }{
		{"the unstamped-equals-unscaled predicate", "NOT (metadata ? 'credit_unit')"},
		{"the blanket multiply remedy", "UPDATE x10000"},
	} {
		if strings.Contains(raw, banned.needle) {
			t.Errorf("the rescale migration still documents %s (%q); a missing stamp is evidence of a writer that does not stamp, not of an old unit (issue #1704)",
				banned.what, banned.needle)
		}
	}

	// The replacement has to be reachable from here, or the correction is just
	// a deletion and the next operator writes the naive query again.
	if !strings.Contains(raw, "credit_unit_straggler_candidates") {
		t.Error("the rescale migration's post-deploy section must point at public.credit_unit_straggler_candidates, the only sanctioned detector")
	}
}

// TestStragglerMigrationWritesMetadataOnly is the money bound on the backfill:
// it stamps rows whose unit is already known and must never touch an amount.
// Every SET target in the file is required to be metadata, so a credits column
// appearing in one is a failing test rather than a silent repricing.
func TestStragglerMigrationWritesMetadataOnly(t *testing.T) {
	raw, err := os.ReadFile(stragglerMigrationRelPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", stragglerMigrationRelPath, err)
	}
	body := string(raw)

	sets := regexp.MustCompile(`(?i)\bSET\s+([a-z_]+)\s*=`).FindAllStringSubmatch(body, -1)
	if len(sets) == 0 {
		t.Fatal("no UPDATE ... SET found; this migration is supposed to stamp rows")
	}
	for _, m := range sets {
		if strings.ToLower(m[1]) != "metadata" {
			t.Errorf("migration assigns %q; the stamping backfill must write metadata and nothing else", m[1])
		}
	}

	// The stamp values are the two the audit scheme defines. A third spelling
	// would be invisible to every reader that knows only these.
	for _, needle := range []string{"'credit_unit', 'v2-1usd-1e9'", "'credit_unit', 'legacy-1usd-100k-credits'"} {
		if !strings.Contains(body, needle) {
			t.Errorf("migration does not stamp with %s", needle)
		}
	}

	// The detector is a view over money tables. It must not become a
	// PostgREST-readable surface for the published keys.
	for _, needle := range []string{"security_invoker = true", "revoke all on public.credit_unit_straggler_candidates from anon"} {
		if !strings.Contains(body, needle) {
			t.Errorf("migration is missing %q, so the detector view is not locked down like the marker table it reads", needle)
		}
	}
}
