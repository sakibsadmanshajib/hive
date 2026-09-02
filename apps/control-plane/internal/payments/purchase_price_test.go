package payments

// citation-check: allow-unknown-ids. D-064, D-065 and D-066 landed on main
// after the shared checkout this session's guard reads was cut, so the guard
// resolves them as unknown. They are real entries in .wolf/decisions.md on
// main and in this branch's own copy.

import (
	"math/big"
	"testing"
)

// The purchase price is where this product takes its margin (D-065). These
// tests pin the exact arithmetic rather than a boolean, because every defect
// this path has produced was a plausible number rather than a failure.

// TestUSDPurchasePricesOneBlockAtTheMarkup is the headline figure: one billion
// credits, which is one USD of credit value at the D-046 peg, costs 1.06 USD.
func TestUSDPurchasePricesOneBlockAtTheMarkup(t *testing.T) {
	got, err := PriceForCredits(CreditsPerUSD, "")
	if err != nil {
		t.Fatalf("PriceForCredits: %v", err)
	}
	if got.USDCents != 106 {
		t.Fatalf("1e9 credits = %d cents, want 106 (100 cents at the peg plus the 6 percent markup)", got.USDCents)
	}
	if got.LocalMinor != 0 {
		t.Fatalf("a USD purchase carries LocalMinor = %d, want 0: there is no local currency to convert into", got.LocalMinor)
	}
	if got.MarkupRate != PurchaseMarkupRate {
		t.Fatalf("MarkupRate = %q, want %q: the rate recorded on the row must be the rate applied", got.MarkupRate, PurchaseMarkupRate)
	}
}

// TestUSDPurchaseNeverTakesTheFXMarkup is the specific guard issue #1693 asks
// for. The 2.5 percent lives inside an FX rate, and a USD payer converts no
// currency, so no version of that figure may reach their price.
//
// It is checked across the price range rather than on one amount: for every
// quantity below, the USD price is exactly the peg price times the credit
// markup, with no other factor anywhere in it. A 2.5 percent leak would break
// this on almost every input.
func TestUSDPurchaseNeverTakesTheFXMarkup(t *testing.T) {
	for _, credits := range []int64{
		MinPurchaseCredits,
		CreditsPerUSD,
		2 * CreditsPerUSD,
		11 * CreditIncrement,
		MaxPurchaseCreditsStripe,
	} {
		got, err := PriceForCredits(credits, "")
		if err != nil {
			t.Fatalf("PriceForCredits(%d): %v", credits, err)
		}

		// Recomputed independently, in exact rationals, from the two constants
		// a USD purchase is allowed to involve.
		want := new(big.Rat).SetFrac(big.NewInt(credits), big.NewInt(CreditIncrement))
		markup, ok := new(big.Rat).SetString(PurchaseMarkupRate)
		if !ok {
			t.Fatalf("PurchaseMarkupRate %q does not parse", PurchaseMarkupRate)
		}
		want.Mul(want, markup.Add(markup, new(big.Rat).SetInt64(1)))
		wantCents := new(big.Int).Quo(want.Num(), want.Denom()).Int64()

		if got.USDCents != wantCents {
			t.Fatalf("%d credits = %d cents, want %d: a USD price is the peg price times the credit markup and nothing else",
				credits, got.USDCents, wantCents)
		}
	}
}

// TestBDTPurchaseAppliesTheMarkupThenTheRate walks the owner's own worked
// example end to end: a mid rate of 127.00 becomes 130.175 once the 2.5 percent
// is folded in, and a customer buying one billion credits pays 1.06 USD at that
// rate.
//
//	1e9 credits -> 100 cents at the peg
//	            -> 106 cents after the 6 percent markup
//	            -> 106 x 130.175 = 13798.55 paisa
//	            -> 13798 paisa (137.98 BDT), truncated toward zero
func TestBDTPurchaseAppliesTheMarkupThenTheRate(t *testing.T) {
	const effectiveRate = "130.175000" // 127.00 x 1.025, what fx.go stores

	got, err := PriceForCredits(CreditsPerUSD, effectiveRate)
	if err != nil {
		t.Fatalf("PriceForCredits: %v", err)
	}
	if got.USDCents != 106 {
		t.Fatalf("USDCents = %d, want 106: the USD accounting figure is the same whichever rail is used", got.USDCents)
	}
	if got.LocalMinor != 13798 {
		t.Fatalf("LocalMinor = %d paisa, want 13798 (1.06 USD at 130.175)", got.LocalMinor)
	}
}

// TestPurchasePriceTruncatesAtTheMinorUnit pins the rounding direction and the
// unit at the boundary, in both currencies, since both the markup and the FX
// conversion produce fractions.
//
// 110 cents times 1.06 is 116.6 cents exactly, the half-way-and-above case that
// a half-up rule would take to 117 and this one drops to 116. The paisa figure
// is truncated from that same exact 116.6, never from the truncated 116: at a
// rate of 100, converting 116.6 gives 11660 paisa where converting 116 would
// give 11600.
func TestPurchasePriceTruncatesAtTheMinorUnit(t *testing.T) {
	credits := 110 * CreditIncrement

	usd, err := PriceForCredits(credits, "")
	if err != nil {
		t.Fatalf("PriceForCredits: %v", err)
	}
	if usd.USDCents != 116 {
		t.Fatalf("110 cents at the markup = %d cents, want 116: 116.6 truncates toward zero, it does not round up", usd.USDCents)
	}

	bdt, err := PriceForCredits(credits, "100.000000")
	if err != nil {
		t.Fatalf("PriceForCredits: %v", err)
	}
	if bdt.LocalMinor != 11660 {
		t.Fatalf("LocalMinor = %d paisa, want 11660: the local price converts the exact marked-up figure, not the truncated one", bdt.LocalMinor)
	}
}

// TestPurchasePriceNeverFallsBelowThePegPrice states the property that makes
// truncation safe: dropping a fraction of a minor unit can never take the price
// under what the same credits cost before any markup existed.
func TestPurchasePriceNeverFallsBelowThePegPrice(t *testing.T) {
	for credits := MinPurchaseCredits; credits <= MinPurchaseCredits+50*CreditIncrement; credits += CreditIncrement {
		got, err := PriceForCredits(credits, "")
		if err != nil {
			t.Fatalf("PriceForCredits(%d): %v", credits, err)
		}
		if peg := credits / CreditIncrement; got.USDCents < peg {
			t.Fatalf("%d credits priced at %d cents, below the %d cent peg price", credits, got.USDCents, peg)
		}
	}
}

// TestPurchasePriceRefusesAnUnusableRate keeps a bad rate loud. A conversion
// that silently fell back to the un-converted figure would charge a BD customer
// roughly one hundred and thirtieth of the price.
func TestPurchasePriceRefusesAnUnusableRate(t *testing.T) {
	if _, err := PriceForCredits(CreditsPerUSD, "not-a-rate"); err == nil {
		t.Fatal("priced a purchase against an unparseable rate, want an error")
	}
	if _, err := PriceForCredits(-1, ""); err == nil {
		t.Fatal("priced a negative credit quantity, want an error")
	}
	// A zero or negative rate would price a purchase at nothing, or at a
	// negative amount, and neither looks like a failure once it is a row.
	for _, rate := range []string{"0", "0.000000", "-130.175000"} {
		if _, err := PriceForCredits(CreditsPerUSD, rate); err == nil {
			t.Fatalf("priced a purchase at an effective rate of %q, want an error", rate)
		}
	}
}

// TestReturnedMarkupRateReproducesTheAmountItIsRecordedBeside is the guard that
// survives the flip. It never mentions PurchaseMarkupRate: it takes the rate
// the call REPORTS, applies it to the peg price, and requires that to be the
// amount the same call returned, in each currency.
//
// Today, with PurchaseMarkupAppliesToLocalCurrency true, both branches report
// 0.06 and both reproduce. Flip that constant without also reporting the rate
// actually applied and the local-currency case goes red here, instead of going
// quiet and writing a row whose amount cannot be derived from its own stored
// fields. That is the issue #1682 shape, and the recorded rate is what is meant
// to stop it.
func TestReturnedMarkupRateReproducesTheAmountItIsRecordedBeside(t *testing.T) {
	const effectiveRate = "130.175000"

	for _, tc := range []struct {
		name string
		rate string
	}{
		{"usd purchase", ""},
		{"local currency purchase", effectiveRate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PriceForCredits(CreditsPerUSD, tc.rate)
			if err != nil {
				t.Fatalf("PriceForCredits: %v", err)
			}

			reported, ok := new(big.Rat).SetString(got.MarkupRate)
			if !ok {
				t.Fatalf("MarkupRate %q does not parse; a rate nothing can read is not a record", got.MarkupRate)
			}
			amount := new(big.Rat).SetFrac(big.NewInt(CreditsPerUSD), big.NewInt(CreditIncrement))
			amount.Mul(amount, reported.Add(reported, new(big.Rat).SetInt64(1)))

			wantUSD := new(big.Int).Quo(amount.Num(), amount.Denom()).Int64()
			if got.USDCents != wantUSD {
				t.Fatalf("USDCents = %d, but the recorded markup %q reproduces %d; the row would not derive from its own fields",
					got.USDCents, got.MarkupRate, wantUSD)
			}

			if tc.rate == "" {
				return
			}
			rateRat, ok := new(big.Rat).SetString(tc.rate)
			if !ok {
				t.Fatalf("test fixture rate %q does not parse", tc.rate)
			}
			amount.Mul(amount, rateRat)
			wantLocal := new(big.Int).Quo(amount.Num(), amount.Denom()).Int64()
			if got.LocalMinor != wantLocal {
				t.Fatalf("LocalMinor = %d, but the recorded markup %q at the recorded rate reproduces %d",
					got.LocalMinor, got.MarkupRate, wantLocal)
			}
		})
	}
}

// TestMarkupConstantsAreTheOwnerRulings pins the two rates themselves. They are
// the product's headline pricing decision (D-065, D-066) and neither should
// move without the ruling that moves it.
func TestMarkupConstantsAreTheOwnerRulings(t *testing.T) {
	markup, ok := new(big.Rat).SetString(PurchaseMarkupRate)
	if !ok {
		t.Fatalf("PurchaseMarkupRate %q does not parse as a rational", PurchaseMarkupRate)
	}
	if markup.Cmp(big.NewRat(6, 100)) != 0 {
		t.Fatalf("PurchaseMarkupRate = %q, want 0.06 (D-065)", PurchaseMarkupRate)
	}

	fee, ok := new(big.Rat).SetString(FXFeeRate)
	if !ok {
		t.Fatalf("FXFeeRate %q does not parse as a rational", FXFeeRate)
	}
	if fee.Cmp(big.NewRat(25, 1000)) != 0 {
		t.Fatalf("FXFeeRate = %q, want 0.025 (D-066)", FXFeeRate)
	}
}
