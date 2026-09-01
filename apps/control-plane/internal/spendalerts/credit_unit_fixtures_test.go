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
// amount at the pinned rate: 100,000 credits to the paisa, since one paisa is
// one hundredth of a taka and one taka is one hundredth of a USD at 100 BDT per
// USD, and a USD is 1,000,000,000 credits.
//
// The literal is DELIBERATE. It states that relationship independently of the
// constants the code under test uses, which is the only reason an assertion
// built on it can observe an error in either of them. Deriving it from
// payments.CreditsPerUSD and payments.SubunitsPerBDT would make this the exact
// algebraic inverse of payments.CreditsToBDTSubunits, the round trip would be
// the identity for any value of either constant, and every assertion routing
// through this helper would go blind. Measured, not argued: with the derived
// form, a hundredfold error in SubunitsPerBDT left this package entirely green.
func spendCredits(subunits int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(subunits), big.NewInt(100_000))
}
