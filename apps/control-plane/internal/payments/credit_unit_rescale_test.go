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
	// MinPurchaseCredits is deliberately NOT asserted here any more. It used
	// to be pinned to one cent step, which is a bare number standing in for a
	// property nobody had checked: the floor exists to clear the chat
	// authorization hold, and pinned to one cent it was one tenth of one hold
	// (issue #1450). What it must satisfy now is a relationship with a
	// constant in another module, so the assertion lives in
	// purchase_floor_test.go where that constant can be read. All this file
	// still owes it is that it speaks the current unit in whole cent steps.
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
		{"pre-rescale one-cent step", 1_000, false},
		{"pre-rescale one dollar", oldCreditsPerUSD, false},
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

// TestPredefinedTiersAreCentSteps keeps the suggested purchase buttons on
// whole cent multiples of the current unit.
//
// The exact cent values are no longer pinned here. They were {1, 5, 10, 50,
// 100}, and that list is precisely how issue #1450 shipped: the assertion
// matched the constants exactly and so proved only that nobody had retyped
// them, while two of the five tiers could not buy a single chat message. The
// property that matters, every tier at or above the purchase floor, is
// asserted against the floor in purchase_floor_test.go. What stays here is the
// unit: whole cent steps, so a tier could not survive a future rescale
// unscaled.
func TestPredefinedTiersAreCentSteps(t *testing.T) {
	if len(PredefinedTiers) == 0 {
		t.Fatal("PredefinedTiers is empty")
	}
	for i, tier := range PredefinedTiers {
		if tier%CreditIncrement != 0 {
			t.Errorf("tier[%d] = %d is not a whole one-cent step of %d", i, tier, CreditIncrement)
		}
	}
}

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
