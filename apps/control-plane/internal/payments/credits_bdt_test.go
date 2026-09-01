package payments

import (
	"math/big"
	"testing"
)

// TestCreditsToBDTSubunitsRejectsUnitConflation pins the exact live magnitude
// that exposed the defect (issue #1648).
//
// Measured on console-hive.scubed.co on 2026-09-01: the workspace invoice for
// 2026-08-01 to 2026-09-01 aggregated 524,653,338 credits and the console
// rendered ৳5,246,533.38, which is that credit count read as a paisa count.
// Analytics reported $0.525 of spend for the same traffic. At 123.13 BDT per
// USD the honest figure is ৳64.60, so the rendered number was overstated by a
// factor of about eighty thousand.
//
// The assertion is on the magnitude, not on formatting: a conversion that
// still returns the raw credit count fails here even if every string in the
// renderer is correct.
func TestCreditsToBDTSubunitsRejectsUnitConflation(t *testing.T) {
	t.Parallel()

	credits := big.NewInt(524_653_338)
	rate := big.NewRat(12313, 100) // 123.13 BDT per USD

	got, err := CreditsToBDTSubunits(credits, rate)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	want := big.NewInt(6460) // ৳64.60
	if got.Cmp(want) != 0 {
		t.Fatalf("subunits = %s, want %s", got, want)
	}
	if got.Cmp(credits) == 0 {
		t.Fatalf("credits were reinterpreted as subunits: %s", got)
	}
}

// TestCreditsToBDTSubunitsArithmetic covers the conversion contract: the
// credit unit, the subunit factor, rounding, and the degenerate inputs.
func TestCreditsToBDTSubunitsArithmetic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		credits *big.Int
		rate    *big.Rat
		want    *big.Int
	}{
		{
			// One USD of credits at 100 BDT per USD is 100 taka, 10,000 paisa.
			name:    "one usd at a round rate",
			credits: big.NewInt(CreditsPerUSD),
			rate:    big.NewRat(100, 1),
			want:    big.NewInt(10_000),
		},
		{
			name:    "zero credits",
			credits: big.NewInt(0),
			rate:    big.NewRat(123, 1),
			want:    big.NewInt(0),
		},
		{
			// A negative aggregate can only come from a corrupt ledger read.
			// Clamp rather than render a negative invoice line.
			name:    "negative credits clamp to zero",
			credits: big.NewInt(-5_000_000_000),
			rate:    big.NewRat(123, 1),
			want:    big.NewInt(0),
		},
		{
			// 150,000 credits at 100 BDT per USD is exactly 1.5 paisa.
			name:    "exact half rounds up",
			credits: big.NewInt(150_000),
			rate:    big.NewRat(100, 1),
			want:    big.NewInt(2),
		},
		{
			name:    "just under half rounds down",
			credits: big.NewInt(149_999),
			rate:    big.NewRat(100, 1),
			want:    big.NewInt(1),
		},
		{
			// Sub-paisa usage rounds to zero rather than inventing a charge.
			name:    "sub paisa rounds to zero",
			credits: big.NewInt(100),
			rate:    big.NewRat(100, 1),
			want:    big.NewInt(0),
		},
		{
			// Far past int64 paisa: the helper stays in big.Int the whole way.
			name:    "beyond int64 stays exact",
			credits: new(big.Int).Mul(big.NewInt(CreditsPerUSD), big.NewInt(1_000_000_000_000)),
			rate:    big.NewRat(100, 1),
			want:    new(big.Int).Mul(big.NewInt(10_000), big.NewInt(1_000_000_000_000)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CreditsToBDTSubunits(tc.credits, tc.rate)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got.Cmp(tc.want) != 0 {
				t.Fatalf("subunits = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestCreditsToBDTSubunitsRejectsUnusableRate keeps the money path fail-closed:
// an absent or nonsensical rate produces an error, never a number.
func TestCreditsToBDTSubunitsRejectsUnusableRate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		credits *big.Int
		rate    *big.Rat
	}{
		{name: "nil credits", credits: nil, rate: big.NewRat(123, 1)},
		{name: "nil rate", credits: big.NewInt(1), rate: nil},
		{name: "zero rate", credits: big.NewInt(1), rate: big.NewRat(0, 1)},
		{name: "negative rate", credits: big.NewInt(1), rate: big.NewRat(-123, 1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CreditsToBDTSubunits(tc.credits, tc.rate)
			if err == nil {
				t.Fatalf("expected an error, got subunits %v", got)
			}
			if got != nil {
				t.Fatalf("expected no value alongside the error, got %v", got)
			}
		})
	}
}

// TestPlatformUSDBDTRate covers fallback rate resolution: the documented
// default when the environment says nothing, an operator override when it does,
// and a hard error when the override cannot be parsed. A malformed override
// must never fall back to the default silently, because that would bill at a
// rate nobody chose.
func TestPlatformUSDBDTRate(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		t.Setenv(USDBDTRateEnvVar, "")
		got, err := PlatformUSDBDTRate()
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		want, ok := new(big.Rat).SetString(DefaultUSDBDTRate)
		if !ok {
			t.Fatalf("default rate %q does not parse", DefaultUSDBDTRate)
		}
		if got.Rate.Cmp(want) != 0 {
			t.Fatalf("rate = %s, want %s", got.Rate.FloatString(6), want.FloatString(6))
		}
		// The compiled default is nobody's decision, so the source label has to
		// say which arm produced it; callers log that.
		if got.Source != RateSourceDefault {
			t.Fatalf("source = %q, want %q", got.Source, RateSourceDefault)
		}
		if got.Display != "123.130000" {
			t.Fatalf("display = %q, want %q", got.Display, "123.130000")
		}
	})

	t.Run("operator override", func(t *testing.T) {
		t.Setenv(USDBDTRateEnvVar, "110.50")
		got, err := PlatformUSDBDTRate()
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got.Rate.Cmp(big.NewRat(11050, 100)) != 0 {
			t.Fatalf("rate = %s, want 110.50", got.Rate.FloatString(6))
		}
		if got.Source != RateSourceEnv {
			t.Fatalf("source = %q, want %q", got.Source, RateSourceEnv)
		}
	})

	for _, bad := range []string{"not-a-rate", "0", "-12", "1e2", "123.1234567", "1234567890123"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			t.Setenv(USDBDTRateEnvVar, bad)
			got, err := PlatformUSDBDTRate()
			if err == nil {
				t.Fatalf("expected an error for %q, got %s from %s", bad, got.Display, got.Source)
			}
		})
	}
}

// TestParseUSDBDTRateNormalisesToStoredScale is the reproducibility guarantee.
//
// public.invoices.usd_bdt_rate is numeric(18, 6). Postgres does not reject a
// rate with more precision, it rounds it, so a conversion run at 123.1234567
// would be recorded as 123.123457 and recomputing the amounts from the stored
// rate would not reproduce them. The parser refuses anything past the stored
// scale and hands back a Display already at that scale, so the value converted
// at, the value written, and the value Postgres reads back are one string.
func TestParseUSDBDTRateNormalisesToStoredScale(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		display string
	}{
		{raw: "100", display: "100.000000"},
		{raw: "123.13", display: "123.130000"},
		{raw: "123.130000", display: "123.130000"},
		{raw: "129.586500", display: "129.586500"}, // shape of an fx_snapshots effective_rate
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got, err := ParseUSDBDTRate(tc.raw, RateSourceSnapshot)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.raw, err)
			}
			if got.Display != tc.display {
				t.Fatalf("display = %q, want %q", got.Display, tc.display)
			}
			if got.Source != RateSourceSnapshot {
				t.Fatalf("source = %q, want %q", got.Source, RateSourceSnapshot)
			}
			again, err := ParseUSDBDTRate(got.Display, RateSourceSnapshot)
			if err != nil {
				t.Fatalf("reparse %q: %v", got.Display, err)
			}
			if again.Rate.Cmp(got.Rate) != 0 {
				t.Fatalf("round trip changed the rate: %s then %s",
					got.Rate.FloatString(9), again.Rate.FloatString(9))
			}
		})
	}

	if _, err := ParseUSDBDTRate("123.1234567", RateSourceSnapshot); err == nil {
		t.Fatal("expected a rate beyond numeric(18, 6) to be refused")
	}
}

// =============================================================================
// The inverse: BDT subunits to credits (issue #1659)
// =============================================================================

// TestBDTSubunitsToCreditsRejectsUnitConflation pins the magnitude from issue
// #1659, the write-side twin of the defect at the top of this file.
//
// grants.CreateWithLedger wrote input.AmountBDTSubunits.Int64() straight into
// public.credit_ledger_entries.credits_delta. A platform admin granting 100,000
// subunits, that is 1,000 taka or about eight USD, posted 100,000 credits,
// which at 1,000,000,000 credits to the USD is 0.0001 USD of inference. The
// grant was short by about seven orders of magnitude.
//
// The expected value is a LITERAL on purpose. Deriving it from CreditsPerUSD
// and SubunitsPerBDT would make this assertion the exact algebraic inverse of
// the function under test: the round trip would then hold for any value of
// either constant and the assertion would go blind. That was measured on issue
// #1648, where a hundredfold error in SubunitsPerBDT left a whole package
// green.
func TestBDTSubunitsToCreditsRejectsUnitConflation(t *testing.T) {
	t.Parallel()

	subunits := big.NewInt(100_000) // 1,000 taka
	rate := big.NewRat(12313, 100)  // 123.13 BDT per USD

	got, err := BDTSubunitsToCredits(subunits, rate)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	want := big.NewInt(8_121_497_604) // about 8.12 USD of credits
	if got.Cmp(want) != 0 {
		t.Fatalf("credits = %s, want %s", got, want)
	}
	if got.Cmp(subunits) == 0 {
		t.Fatalf("subunits were reinterpreted as credits: %s", got)
	}
}

// TestBDTSubunitsToCreditsArithmetic covers the conversion contract: the credit
// unit, the subunit factor, rounding, and the degenerate inputs. Every want is
// a literal, for the reason given above.
func TestBDTSubunitsToCreditsArithmetic(t *testing.T) {
	t.Parallel()

	beyondInt64, ok := new(big.Int).SetString("10000000000000000000", 10)
	if !ok {
		t.Fatal("test fixture does not parse")
	}

	cases := []struct {
		name     string
		subunits *big.Int
		rate     *big.Rat
		want     *big.Int
	}{
		{
			// 123.13 taka is one USD at the live rate, so it is exactly one
			// USD of credits and nothing rounds.
			name:     "one usd of taka at the live rate",
			subunits: big.NewInt(12_313),
			rate:     big.NewRat(12313, 100),
			want:     big.NewInt(1_000_000_000),
		},
		{
			// 1,000 taka at 100 BDT per USD is ten USD.
			name:     "a round rate",
			subunits: big.NewInt(100_000),
			rate:     big.NewRat(100, 1),
			want:     big.NewInt(10_000_000_000),
		},
		{
			name:     "one paisa at a round rate",
			subunits: big.NewInt(1),
			rate:     big.NewRat(100, 1),
			want:     big.NewInt(100_000),
		},
		{
			// One paisa at 1,280 BDT per USD is exactly 7,812.5 credits. The
			// rate is chosen because it is the smallest plausibly-shaped one
			// that lands on an exact half; the rule under test is the rounding
			// direction, not the rate.
			name:     "exact half rounds up",
			subunits: big.NewInt(1),
			rate:     big.NewRat(1280, 1),
			want:     big.NewInt(7_813),
		},
		{
			name:     "zero subunits",
			subunits: big.NewInt(0),
			rate:     big.NewRat(12313, 100),
			want:     big.NewInt(0),
		},
		{
			// Credits are a billion to the USD, so a grant large enough to be
			// refused by the int64 ledger column is still exact here. The
			// caller is the one that has to refuse it, and does.
			name:     "beyond int64 stays exact",
			subunits: big.NewInt(100_000_000_000_000),
			rate:     big.NewRat(100, 1),
			want:     beyondInt64,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := BDTSubunitsToCredits(tc.subunits, tc.rate)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got.Cmp(tc.want) != 0 {
				t.Fatalf("credits = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestBDTSubunitsToCreditsRejectsUnusableInput keeps the WRITE side fail-closed.
//
// This is where it differs from CreditsToBDTSubunits deliberately. That one
// clamps a negative aggregate to zero, because it renders a display figure from
// a ledger read and a corrupt read must not print a negative invoice line. This
// one posts money into an append-only ledger, so a negative amount is refused
// rather than silently turned into a grant of nothing.
func TestBDTSubunitsToCreditsRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		subunits *big.Int
		rate     *big.Rat
	}{
		{name: "nil subunits", subunits: nil, rate: big.NewRat(123, 1)},
		{name: "negative subunits", subunits: big.NewInt(-100_000), rate: big.NewRat(123, 1)},
		{name: "nil rate", subunits: big.NewInt(1), rate: nil},
		{name: "zero rate", subunits: big.NewInt(1), rate: big.NewRat(0, 1)},
		{name: "negative rate", subunits: big.NewInt(1), rate: big.NewRat(-123, 1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := BDTSubunitsToCredits(tc.subunits, tc.rate)
			if err == nil {
				t.Fatalf("expected an error, got credits %v", got)
			}
			if got != nil {
				t.Fatalf("expected no value alongside the error, got %v", got)
			}
		})
	}
}

// TestBDTSubunitsToCreditsIsNotTheIdentity states the invariant in its own
// right, across the whole plausible rate band rather than at one pinned rate.
// No rate a human would configure makes a subunit count and a credit count the
// same number, so any implementation that returns its input fails here.
func TestBDTSubunitsToCreditsIsNotTheIdentity(t *testing.T) {
	t.Parallel()

	subunits := big.NewInt(100_000)
	for _, raw := range []string{"1", "80", "123.13", "150", "1000"} {
		rate, ok := new(big.Rat).SetString(raw)
		if !ok {
			t.Fatalf("fixture rate %q does not parse", raw)
		}
		got, err := BDTSubunitsToCredits(subunits, rate)
		if err != nil {
			t.Fatalf("convert at %s: %v", raw, err)
		}
		if got.Cmp(subunits) == 0 {
			t.Fatalf("at rate %s the conversion returned its input: %s", raw, got)
		}
	}
}
