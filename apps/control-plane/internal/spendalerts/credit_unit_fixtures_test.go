package spendalerts

import (
	"math/big"
	"os"
	"testing"
)

// =============================================================================
// Credit unit fixtures (issue #1648)
//
// The ledger stores credits, one billionth of a USD each, and a budget cap is
// typed in taka. The evaluator converts between the two at the platform rate,
// so these tests pin that rate rather than inheriting whatever the deployment
// default happens to be, and express every seeded spend as the credit quantity
// that is worth the taka figure the assertion talks about.
//
// At 100 BDT per USD, one paisa is 100,000 credits.
// =============================================================================

func TestMain(m *testing.M) {
	if err := os.Setenv("HIVE_USD_BDT_RATE", "100"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// spendCredits returns the credit quantity worth the supplied BDT subunit
// amount at the pinned rate above.
func spendCredits(subunits int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(subunits), big.NewInt(100_000))
}
