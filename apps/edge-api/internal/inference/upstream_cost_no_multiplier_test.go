package inference

import (
	"math/big"
	"testing"
)

// A burn costs what the provider charged, and nothing more. The 1.4 margin
// multiplier that used to sit on this path was retired on 2026-09-02 when
// margin moved to the purchase price, and these tests are what stop it, or any
// other factor, coming back.
//
// They assert an IDENTITY rather than a bound: credits equal cost times the
// peg, exactly, for costs spanning six orders of magnitude. A reintroduced
// multiplier of any size fails every case, and so does a factor introduced
// anywhere else in the chain this function is the end of.

// TestCreditsForUpstreamCostCarriesNoMultiplier pins the identity itself.
func TestCreditsForUpstreamCostCarriesNoMultiplier(t *testing.T) {
	for _, costUSD := range []string{
		"0.000001",
		"0.0001234",
		"0.0123456",
		"0.1",
		"1",
		"9.5",
	} {
		cost, ok := new(big.Rat).SetString(costUSD)
		if !ok {
			t.Fatalf("test fixture %q does not parse", costUSD)
		}

		got, err := CreditsForUpstreamCost(cost)
		if err != nil {
			t.Fatalf("CreditsForUpstreamCost(%s): %v", costUSD, err)
		}

		// The whole expectation, written out: cost at the peg, rounded half up
		// once. No margin, no fee, no adjustment.
		want := new(big.Rat).Mul(cost, new(big.Rat).SetInt64(CreditsPerUSD))
		quotient, remainder := new(big.Int).QuoRem(want.Num(), want.Denom(), new(big.Int))
		if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(want.Denom()) >= 0 {
			quotient.Add(quotient, big.NewInt(1))
		}

		if got != quotient.Int64() {
			t.Fatalf("a provider cost of $%s burned %d credits, want %d. A multiplier has been reintroduced somewhere in this chain; margin belongs at the purchase price, not on a burn.",
				costUSD, got, quotient.Int64())
		}
	}
}

// TestOneDollarOfProviderCostBurnsOneBillionCredits is the same property stated
// as the single figure a reader can check by eye, and it is the one that goes
// red at 1,400,000,000 if the old 7/5 factor returns.
func TestOneDollarOfProviderCostBurnsOneBillionCredits(t *testing.T) {
	got, err := CreditsForUpstreamCost(big.NewRat(1, 1))
	if err != nil {
		t.Fatalf("CreditsForUpstreamCost: %v", err)
	}
	if got != CreditsPerUSD {
		t.Fatalf("$1.00 of provider cost burned %d credits, want %d (the peg, with no margin factor)", got, CreditsPerUSD)
	}
}
