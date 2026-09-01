package grants

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// =============================================================================
// Issue #1659: the grant path wrote a BDT subunit count into credits_delta.
//
// These are in `package grants` rather than `package grants_test` on purpose:
// the conversion this file pins is the step CreateWithLedger takes between the
// admin's taka amount and the ledger's credit quantity, and it is unexported
// because nothing outside this package should be able to post a grant without
// it. The suites in service_test.go and http_test.go go through a fake
// repository, so they never reach this step at all, which is exactly why the
// defect survived them.
//
// Every expected value here is a LITERAL. Deriving one from payments.
// CreditsPerUSD and payments.SubunitsPerBDT would make the assertion the
// algebraic inverse of the function under test, and the round trip would hold
// for any value of either constant. That was measured on issue #1648: with the
// derived form, a hundredfold error in SubunitsPerBDT left a whole package
// green.
// =============================================================================

// TestCreditsForGrantDoesNotPostSubunitsAsCredits is the regression pin for
// the defect itself, at the exact magnitude issue #1659 reports.
//
// A platform admin grants 100,000 subunits, that is 1,000 taka, roughly eight
// USD. At the platform's compiled default rate of 123.13 BDT per USD, the
// ledger must receive about 8.12 billion credits. Before the fix it received
// 100,000, which is 0.0001 USD of inference.
func TestCreditsForGrantDoesNotPostSubunitsAsCredits(t *testing.T) {
	// Not t.Parallel: PlatformUSDBDTRate reads the process environment and
	// t.Setenv forbids it, which is the point. The rate is resolved per grant
	// so an operator override takes effect without a redeploy.
	t.Setenv(payments.USDBDTRateEnvVar, "")

	subunits := big.NewInt(100_000)
	credits, rate, err := creditsForGrant(subunits)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	const want int64 = 8_121_497_604
	if credits != want {
		t.Fatalf("credits_delta = %d, want %d", credits, want)
	}
	if credits == subunits.Int64() {
		t.Fatalf("subunits were posted as credits: %d", credits)
	}
	if rate.Display != "123.130000" {
		t.Fatalf("rate = %q, want the compiled default", rate.Display)
	}
	if rate.Source != payments.RateSourceDefault {
		t.Fatalf("rate source = %q, want %q", rate.Source, payments.RateSourceDefault)
	}
}

// TestCreditsForGrantHonoursTheOperatorRate proves the resolved rate is the one
// actually applied, not decoration recorded next to a fixed conversion.
func TestCreditsForGrantHonoursTheOperatorRate(t *testing.T) {
	t.Setenv(payments.USDBDTRateEnvVar, "100")

	credits, rate, err := creditsForGrant(big.NewInt(100_000))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	// 1,000 taka at 100 BDT per USD is ten USD, and a USD is a billion credits.
	const want int64 = 10_000_000_000
	if credits != want {
		t.Fatalf("credits_delta = %d, want %d", credits, want)
	}
	if rate.Display != "100.000000" || rate.Source != payments.RateSourceEnv {
		t.Fatalf("rate = %q from %q, want the operator override", rate.Display, rate.Source)
	}
}

// TestCreditsForGrantFailsClosed covers every arm that must produce no grant
// at all rather than a plausible wrong number. A grant is an append to an
// immutable ledger: a wrong row cannot be taken back, only compensated.
func TestCreditsForGrantFailsClosed(t *testing.T) {
	t.Run("unparseable operator rate", func(t *testing.T) {
		t.Setenv(payments.USDBDTRateEnvVar, "not-a-rate")
		if credits, _, err := creditsForGrant(big.NewInt(100_000)); err == nil {
			t.Fatalf("expected an error, got %d credits", credits)
		}
	})

	t.Run("operator rate of zero", func(t *testing.T) {
		t.Setenv(payments.USDBDTRateEnvVar, "0")
		if credits, _, err := creditsForGrant(big.NewInt(100_000)); err == nil {
			t.Fatalf("expected an error, got %d credits", credits)
		}
	})

	t.Run("nil amount", func(t *testing.T) {
		t.Setenv(payments.USDBDTRateEnvVar, "100")
		if credits, _, err := creditsForGrant(nil); err == nil {
			t.Fatalf("expected an error, got %d credits", credits)
		}
	})

	t.Run("negative amount", func(t *testing.T) {
		t.Setenv(payments.USDBDTRateEnvVar, "100")
		if credits, _, err := creditsForGrant(big.NewInt(-1)); err == nil {
			t.Fatalf("expected an error, got %d credits", credits)
		}
	})

	// A positive grant that rounds away to nothing must not answer 201
	// Created over an empty ledger row. Reaching this means the configured
	// rate is wrong by nine or more orders of magnitude, which should stop
	// the grant rather than hide inside it (D-034).
	t.Run("a grant that rounds to zero credits", func(t *testing.T) {
		t.Setenv(payments.USDBDTRateEnvVar, "999999999999")
		credits, _, err := creditsForGrant(big.NewInt(1))
		if err == nil {
			t.Fatalf("expected a refusal, got %d credits", credits)
		}
		if credits != 0 {
			t.Fatalf("expected no value alongside the error, got %d", credits)
		}
	})

	// The overflow arm is the reason this returns an error rather than an
	// int64 taken from big.Int.Int64(). A subunit amount that fits the
	// credit_grants bigint column becomes a credit quantity roughly ten
	// million times larger, so the ledger's own bigint is the binding limit,
	// and Int64() on an out-of-range big.Int returns an undefined value with
	// no error: silently the wrong sign or magnitude, posted as money. That is
	// the narrowing tracked in issue #1547 and it is not repeated here.
	t.Run("credit quantity beyond the ledger column", func(t *testing.T) {
		t.Setenv(payments.USDBDTRateEnvVar, "100")
		subunits := big.NewInt(math.MaxInt64) // fits credit_grants, not the ledger
		credits, _, err := creditsForGrant(subunits)
		if err == nil {
			t.Fatalf("expected an overflow error, got %d credits", credits)
		}
		if credits != 0 {
			t.Fatalf("expected no value alongside the error, got %d", credits)
		}
		// The sentinel is what earns this a 400 instead of a 500: the value
		// came from the request body, and an admin who typed extra zeros is
		// owed "too large", not a generic server failure.
		if !errors.Is(err, ErrAmountTooLarge) {
			t.Fatalf("error = %v, want it to wrap ErrAmountTooLarge", err)
		}
	})
}
