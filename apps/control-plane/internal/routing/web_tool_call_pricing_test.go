package routing

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Offline guard over the web tool pricing migration (issue #1695).
//
// Nothing in the running system re-derives these two figures, so a wrong one
// is a silent mispricing that ships. The assertion is positional, against a
// named column of a named row, not "does this number appear somewhere in the
// file": catalog_alias_pricing_test.go records three real mispricings that
// passed a presence check, including two numbers swapped inside one tuple.
//
// Same known limit as that file, stated rather than left to be discovered:
// this is pinned to ONE migration filename. A later migration that reprices
// these aliases inherits none of this and passes while doing anything it
// likes. The way out when that day comes is the same one: assert at the
// database level, where catalog_pricing_integration_test.go already lives.

const webToolPricingMigrationRelPath = "supabase/migrations/20260902_01_web_tool_call_pricing.sql"

// The owner's decision of 2026-09-02, at the D-046 peg of 1 USD = 1e9 credits,
// with no margin multiplier because margin is taken at purchase (#1693):
//
//	web_search  0.0001 USD per call -> 100000 credits per call
//	web_fetch   0.0002 USD per call -> 200000 credits per call
//
// The column is quoted per MILLION units, like every other price_unit, so each
// figure is multiplied by 1e6 before it lands in output_price_credits.
// Integers only, no float64 anywhere near a money figure (repo convention):
// 0.0001 USD is one ten-thousandth of the peg, and 0.0002 USD is two.
const (
	creditsPerUSDV2           = int64(1_000_000_000)
	webSearchCreditsPerCall   = creditsPerUSDV2 / 10_000
	webFetchCreditsPerCall    = 2 * (creditsPerUSDV2 / 10_000)
	creditsPerMillionMultiple = int64(1_000_000)
)

func readWebToolPricingMigration(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, webToolPricingMigrationRelPath)
}

// aliasTuple extracts one VALUES tuple by its leading alias_id literal.
func aliasTuple(t *testing.T, sql, aliasID string) string {
	t.Helper()
	marker := "'" + aliasID + "',"
	start := strings.Index(sql, marker)
	if start < 0 {
		t.Fatalf("%s: no VALUES tuple for alias %s", webToolPricingMigrationRelPath, aliasID)
	}
	end := strings.Index(sql[start:], ")")
	if end < 0 {
		t.Fatalf("%s: unterminated tuple for alias %s", webToolPricingMigrationRelPath, aliasID)
	}
	return sql[start : start+end]
}

func TestWebToolMigrationPricesBothToolsPerCall(t *testing.T) {
	sql := readWebToolPricingMigration(t)

	// The column order the INSERT declares, which is what makes a positional
	// read of the tuple meaningful at all. Asserted first, so a reordered
	// column list fails here rather than silently moving what the checks below
	// are reading.
	wantColumns := []string{
		"alias_id", "owned_by", "display_name", "summary", "visibility",
		"lifecycle", "capability_badges", "input_price_credits",
		"output_price_credits", "cache_read_price_credits",
		"cache_write_price_credits", "price_unit", "pricing_mode",
	}
	insertStart := strings.Index(sql, "INSERT INTO public.model_aliases")
	if insertStart < 0 {
		t.Fatal("migration does not INSERT into public.model_aliases")
	}
	valuesStart := strings.Index(sql[insertStart:], ") VALUES")
	if valuesStart < 0 {
		t.Fatal("migration INSERT has no VALUES clause")
	}
	columnList := sql[insertStart : insertStart+valuesStart]
	for i := 1; i < len(wantColumns); i++ {
		prev := strings.Index(columnList, wantColumns[i-1])
		cur := strings.Index(columnList, wantColumns[i])
		if prev < 0 || cur < 0 || cur < prev {
			t.Fatalf("column %s does not follow %s in the INSERT list", wantColumns[i], wantColumns[i-1])
		}
	}

	cases := []struct {
		alias         string
		creditsPerCal int64
	}{
		{"hive-web-search", webSearchCreditsPerCall},
		{"hive-web-fetch", webFetchCreditsPerCall},
	}
	for _, tc := range cases {
		tuple := aliasTuple(t, sql, tc.alias)
		fields := tupleFields(tuple)
		if len(fields) < 13 {
			t.Fatalf("%s: tuple has %d fields, want at least 13: %s", tc.alias, len(fields), tuple)
		}
		if got := fields[4]; got != "'internal'" {
			t.Errorf("%s: visibility = %s, want 'internal' so it can never appear in a model list", tc.alias, got)
		}
		if got := fields[7]; got != "0" {
			t.Errorf("%s: input_price_credits = %s, want 0 (model_aliases_single_unit_price)", tc.alias, got)
		}
		want := tc.creditsPerCal * creditsPerMillionMultiple
		if got := fields[8]; got != strconv.FormatInt(want, 10) {
			t.Errorf("%s: output_price_credits = %s, want %d (%d credits per call, quoted per million)",
				tc.alias, got, want, tc.creditsPerCal)
		}
		if got := fields[11]; got != "'calls'" {
			t.Errorf("%s: price_unit = %s, want 'calls'", tc.alias, got)
		}
		if got := fields[12]; got != "'fixed'" {
			t.Errorf("%s: pricing_mode = %s, want 'fixed': there is no upstream cost report for a tool call", tc.alias, got)
		}
	}
}

// The CHECK has to be widened, not merely re-added when absent: the constraint
// already exists on every deployment with the old three-value list, so an
// IF NOT EXISTS guard would leave it in place and the INSERT would fail.
func TestWebToolMigrationWidensThePriceUnitCheck(t *testing.T) {
	sql := readWebToolPricingMigration(t)
	if !strings.Contains(sql, "DROP CONSTRAINT IF EXISTS model_aliases_price_unit_allowed") {
		t.Error("migration does not drop the existing price_unit CHECK before widening it")
	}
	widened := regexp.MustCompile(`CHECK \(price_unit IN \('tokens', 'characters', 'seconds', 'calls'\)\)`)
	if !widened.MatchString(sql) {
		t.Error("migration does not recreate the price_unit CHECK with 'calls' alongside the three existing units")
	}
}

// tupleFields splits a VALUES tuple on top-level commas, so a quoted string or
// a jsonb literal containing a comma stays one field.
func tupleFields(tuple string) []string {
	var (
		fields  []string
		current strings.Builder
		inQuote bool
	)
	for _, r := range tuple {
		switch {
		case r == '\'':
			inQuote = !inQuote
			current.WriteRune(r)
		case r == ',' && !inQuote:
			fields = append(fields, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		fields = append(fields, strings.TrimSpace(current.String()))
	}
	return fields
}
