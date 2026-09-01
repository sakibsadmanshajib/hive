package payments

import (
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
)

// =============================================================================
// Credits to BDT (issue #1648)
//
// A Hive credit is one billionth of a USD-equivalent (CreditsPerUSD above). A
// BDT subunit is one paisa, one hundredth of a taka. They are different units
// and the step between them is an exchange rate, never the identity.
//
// Before this file existed, two ledger readers took `SUM(-credits_delta)` and
// stored the result straight into a column named `bdt_subunits`: the invoice
// aggregator and the budget month-to-date reader. Both then rendered that
// credit count as paisa, which overstated a customer's August 2026 spend by
// about eighty thousand times on the live console. The conversion below is the
// one place either of them is allowed to cross the unit boundary.
//
// Everything here is math/big. A float64 cannot hold nine significant digits
// of credits and a rate at the same time without drifting, which is exactly
// the corruption the repository-wide math/big rule exists to prevent.
// =============================================================================

const (
	// SubunitsPerBDT is the number of paisa in one taka.
	SubunitsPerBDT int64 = 100

	// USDBDTRateEnvVar names the operator override for the platform USD to BDT
	// conversion rate, as a plain decimal string such as "123.13".
	USDBDTRateEnvVar = "HIVE_USD_BDT_RATE"

	// DefaultUSDBDTRate is the rate used when the environment says nothing.
	//
	// 123.13 BDT per USD, the mid-market rate published on 2026-09-01. It is a
	// platform rate rather than a live quote on purpose: an invoice must be
	// reproducible from the ledger months later, and a monthly cron that has to
	// reach an FX API before it can bill is a cron that stops billing the first
	// time that API is down. Operators who want a different rate set
	// USDBDTRateEnvVar; the rate actually used is recorded on each invoice row.
	DefaultUSDBDTRate = "123.13"
)

// plainDecimal matches the only rate shape this package accepts: digits, with
// an optional fractional part. Exponent notation and signs are refused because
// in a money configuration value they are far likelier to be a typo than an
// intent, and a rate is never negative.
var plainDecimal = regexp.MustCompile(`^\d+(\.\d+)?$`)

// PlatformUSDBDTRate resolves the USD to BDT rate as an exact rational plus the
// decimal string it came from, which callers persist so the arithmetic on a
// stored invoice stays reproducible.
//
// Fail closed: an override that cannot be parsed is an error, never a silent
// fallback to the default. Falling back would bill at a rate nobody chose and
// would hide the misconfiguration behind plausible-looking numbers.
func PlatformUSDBDTRate() (*big.Rat, string, error) {
	raw := strings.TrimSpace(os.Getenv(USDBDTRateEnvVar))
	if raw == "" {
		raw = DefaultUSDBDTRate
	}
	if !plainDecimal.MatchString(raw) {
		return nil, "", fmt.Errorf("payments: %s must be a plain positive decimal, got %q", USDBDTRateEnvVar, raw)
	}
	rate, ok := new(big.Rat).SetString(raw)
	if !ok || rate.Sign() <= 0 {
		return nil, "", fmt.Errorf("payments: %s must be a positive rate, got %q", USDBDTRateEnvVar, raw)
	}
	return rate, raw, nil
}

// CreditsToBDTSubunits converts a credit quantity into BDT subunits at the
// supplied rate:
//
//	subunits = credits / CreditsPerUSD * rate * SubunitsPerBDT
//
// The whole expression is one exact rational, rounded half up exactly once at
// the end, so no intermediate step can round and drift. A negative aggregate
// (only reachable from a corrupt ledger read) clamps to zero rather than
// producing a negative invoice line. A nil quantity or an unusable rate is an
// error and yields no value at all.
func CreditsToBDTSubunits(credits *big.Int, rate *big.Rat) (*big.Int, error) {
	if credits == nil {
		return nil, fmt.Errorf("payments: credits must be non-nil")
	}
	if rate == nil || rate.Sign() <= 0 {
		return nil, fmt.Errorf("payments: usd to bdt rate must be positive")
	}
	if credits.Sign() <= 0 {
		return new(big.Int), nil
	}

	exact := new(big.Rat).SetInt(credits)
	exact.Mul(exact, rate)
	exact.Mul(exact, new(big.Rat).SetFrac64(SubunitsPerBDT, CreditsPerUSD))
	return ratRoundHalfUp(exact), nil
}

// ratRoundHalfUp rounds a non-negative rational to the nearest integer, halves
// going up: floor((2n + d) / 2d).
func ratRoundHalfUp(r *big.Rat) *big.Int {
	num := new(big.Int).Lsh(r.Num(), 1)   // 2n
	num.Add(num, r.Denom())               // 2n + d
	den := new(big.Int).Lsh(r.Denom(), 1) // 2d
	return num.Div(num, den)
}
