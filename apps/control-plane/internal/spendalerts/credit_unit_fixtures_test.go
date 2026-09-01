package spendalerts

import (
	"math/big"
	"os"
	"strconv"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// =============================================================================
// Credit unit fixtures (issue #1648)
//
// The ledger stores credits, one billionth of a USD each, and a budget cap is
// typed in taka. The evaluator converts between the two at the platform rate,
// so these tests pin that rate rather than inheriting whatever the deployment
// default happens to be, and express every seeded spend as the credit quantity
// that is worth the taka figure the assertion talks about.
// =============================================================================

// fixtureRateBDTPerUSD is the rate these tests pin through the environment.
const fixtureRateBDTPerUSD int64 = 100

func TestMain(m *testing.M) {
	if err := os.Setenv(payments.USDBDTRateEnvVar, strconv.FormatInt(fixtureRateBDTPerUSD, 10)); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// spendCredits returns the credit quantity worth the supplied BDT subunit
// amount at the pinned rate.
//
// Derived from the constants rather than written out as a multiplier, so an
// error in payments.CreditsPerUSD or payments.SubunitsPerBDT changes both the
// fixture and the code under test in the same direction and cannot cancel
// itself out. That absorption is the one thing this suite exists to catch, and
// a literal 100_000 here would have hidden it.
func spendCredits(subunits int64) *big.Int {
	credits := new(big.Int).Mul(big.NewInt(subunits), big.NewInt(payments.CreditsPerUSD))
	return credits.Div(credits, big.NewInt(fixtureRateBDTPerUSD*payments.SubunitsPerBDT))
}
