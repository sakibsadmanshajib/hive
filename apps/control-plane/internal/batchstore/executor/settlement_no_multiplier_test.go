package executor

import (
	"math/big"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// The batch settlement path is the mirror of the sync one, so it gets the
// mirror of the sync path's invariant: a line costs what the provider charged
// and nothing more. Both were multiplying by 1.4 until 2026-09-02, and if only
// one of them is repaired the two pricing modes disagree, which is exactly the
// failure issue #1692 names.

// TestBatchSettlementCarriesNoMultiplier pins the identity for a spread of
// costs. A reintroduced factor of any size fails every case.
func TestBatchSettlementCarriesNoMultiplier(t *testing.T) {
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

		got, err := creditsForUpstreamCost(cost)
		if err != nil {
			t.Fatalf("creditsForUpstreamCost(%s): %v", costUSD, err)
		}

		want := new(big.Rat).Mul(cost, new(big.Rat).SetInt64(creditsPerUSD))
		wantCredits, ok := roundHalfUp(want)
		if !ok {
			t.Fatalf("test recomputation of %s overflowed", costUSD)
		}

		if got != wantCredits {
			t.Fatalf("a provider cost of $%s settled at %d credits, want %d. A multiplier has been reintroduced on the batch path; margin belongs at the purchase price, not on a burn.",
				costUSD, got, wantCredits)
		}
	}
}

// TestOneDollarOfBatchCostSettlesAtOneBillionCredits states the same property as
// the figure a reader can check by eye, and binds it to the payments package's
// own peg rather than to this package's restatement of it.
func TestOneDollarOfBatchCostSettlesAtOneBillionCredits(t *testing.T) {
	got, err := creditsForUpstreamCost(big.NewRat(1, 1))
	if err != nil {
		t.Fatalf("creditsForUpstreamCost: %v", err)
	}
	if got != payments.CreditsPerUSD {
		t.Fatalf("$1.00 of provider cost settled at %d credits, want %d (the peg, with no margin factor)", got, payments.CreditsPerUSD)
	}
}
