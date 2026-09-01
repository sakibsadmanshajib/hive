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
//
// WHERE THE RATE COMES FROM, and why this file is not a second FX source.
// FXService in fx.go is the product's FX authority: it fetches the XE mid
// rate, applies FXFeeRate, and persists the result to public.fx_snapshots,
// which is the rate a BD customer actually paid at checkout
// (service.go usdCentsToLocalPaisa). An invoice for consuming those credits
// has to reconcile against that receipt, so the invoice service asks for the
// account's most recent snapshot FIRST and only falls back to the platform
// rate below when the account has never transacted through a BDT rail. The
// fallback exists because a monthly billing cron cannot depend on an external
// API being reachable, and because a workspace can consume credits it was
// granted rather than bought, in which case no snapshot exists at all.
// =============================================================================

const (
	// SubunitsPerBDT is the number of paisa in one taka.
	SubunitsPerBDT int64 = 100

	// USDBDTRateEnvVar names the operator override for the platform USD to BDT
	// conversion rate, as a plain decimal string such as "123.13".
	USDBDTRateEnvVar = "HIVE_USD_BDT_RATE"

	// DefaultUSDBDTRate is the rate used when the account has no FX snapshot
	// and the environment says nothing.
	//
	// 123.13 BDT per USD, the mid-market rate published on 2026-09-01. It is a
	// last resort, not the product's FX opinion: an account that bought credits
	// through a BDT rail is invoiced at the rate it bought them at. Review this
	// constant whenever the market has moved by more than a few percent, and at
	// minimum once every six months; the rate each invoice actually used is on
	// the row (public.invoices.usd_bdt_rate), so a stale constant is visible in
	// the data rather than only in this comment.
	DefaultUSDBDTRate = "123.13"

	// USDBDTRateScale is the number of fractional digits a rate carries
	// everywhere it is parsed, stored or compared. It matches the scale of
	// public.invoices.usd_bdt_rate, numeric(18, 6), so the string this package
	// hands out is byte-identical to the one Postgres reads back. Without that,
	// a rate configured with more precision would convert at one number and be
	// recorded as another, and the row would no longer reproduce its own
	// amounts.
	USDBDTRateScale = 6

	// Rate sources, reported so a log line or an audit can say which arm
	// produced the number rather than leaving the default invisible.
	RateSourceSnapshot = "fx_snapshot"
	RateSourceEnv      = "env"
	RateSourceDefault  = "default"
)

// plainDecimal matches the only rate shape this package accepts: up to twelve
// integer digits and up to six fractional ones, which is exactly what
// numeric(18, 6) holds. Exponent notation and signs are refused because in a
// money configuration value they are far likelier to be a typo than an intent,
// and a rate is never negative. Bounding the shape here rather than at the
// database keeps the failure at configuration time instead of at insert time,
// after a PDF has already been written to storage.
var plainDecimal = regexp.MustCompile(`^\d{1,12}(\.\d{1,6})?$`)

// USDBDTRate is a resolved conversion rate plus the provenance of the number.
//
// Display is the rate at USDBDTRateScale digits, the exact string persisted on
// an invoice, so the value this package converted at, the value stored, and the
// value Postgres reads back are one string.
type USDBDTRate struct {
	Rate    *big.Rat
	Display string
	Source  string
}

// ParseUSDBDTRate validates and normalises a rate string from any source: the
// operator environment, an fx_snapshots row, or the constant above.
//
// Fail closed: anything that is not a plain positive decimal within the stored
// scale is an error, never a silent fallback. Falling back would bill at a rate
// nobody chose and would hide the misconfiguration behind plausible numbers.
func ParseUSDBDTRate(raw, source string) (USDBDTRate, error) {
	raw = strings.TrimSpace(raw)
	if !plainDecimal.MatchString(raw) {
		return USDBDTRate{}, fmt.Errorf(
			"payments: usd to bdt rate from %s must be a plain positive decimal with at most %d fractional digits, got %q",
			source, USDBDTRateScale, raw)
	}
	rate, ok := new(big.Rat).SetString(raw)
	if !ok || rate.Sign() <= 0 {
		return USDBDTRate{}, fmt.Errorf("payments: usd to bdt rate from %s must be positive, got %q", source, raw)
	}
	return USDBDTRate{
		Rate:    rate,
		Display: rate.FloatString(USDBDTRateScale),
		Source:  source,
	}, nil
}

// PlatformUSDBDTRate resolves the fallback rate: the operator override in
// USDBDTRateEnvVar, or DefaultUSDBDTRate when that is unset. Callers that can
// reach an account's own FX snapshot should prefer it and use this only when
// there is none; see the file comment.
//
// The returned Source distinguishes the two arms so callers can log which one
// produced the number. Production ships the variable empty, so "default" is the
// common answer and it should be visible rather than assumed.
func PlatformUSDBDTRate() (USDBDTRate, error) {
	if raw := strings.TrimSpace(os.Getenv(USDBDTRateEnvVar)); raw != "" {
		return ParseUSDBDTRate(raw, RateSourceEnv)
	}
	return ParseUSDBDTRate(DefaultUSDBDTRate, RateSourceDefault)
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

// ratRoundHalfUp rounds a NON-NEGATIVE rational to the nearest integer, halves
// going up: floor((2n + d) / 2d).
//
// Non-negative only. big.Int.Div floors toward negative infinity, so a negative
// rational would round half DOWN here. The guard that keeps the input
// non-negative lives in CreditsToBDTSubunits, not in this function, so any new
// caller has to bring its own.
func ratRoundHalfUp(r *big.Rat) *big.Int {
	num := new(big.Int).Lsh(r.Num(), 1)   // 2n
	num.Add(num, r.Denom())               // 2n + d
	den := new(big.Int).Lsh(r.Denom(), 1) // 2d
	return num.Div(num, den)
}

// BDTSubunitsToCredits converts a BDT subunit amount into a credit quantity at
// the supplied rate, the exact inverse of CreditsToBDTSubunits:
//
//	credits = subunits / SubunitsPerBDT / rate * CreditsPerUSD
//
// The whole expression is one exact rational, rounded half up exactly once at
// the end, so no intermediate step can round and drift.
//
// This is the WRITE direction (issue #1659). A caller is posting money into an
// append-only ledger rather than rendering a figure on a screen, so it is
// stricter than its inverse in one deliberate way: a negative amount is an
// error here, where a negative aggregate on the read side clamps to zero. A
// negative credit posted as a "grant" is a debit wearing the wrong entry_type,
// and no caller has one; clamping it to zero would instead post a grant of
// nothing and report success. Zero is allowed through as zero, since the only
// caller has already rejected a zero amount with its own error and a second
// opinion here would just be a different message for the same refusal.
//
// The result is a *big.Int and not an int64 on purpose. Credits are a billion
// to the USD, so a subunit amount well inside the credit_grants bigint column
// converts to a credit quantity CreditsPerUSD / SubunitsPerBDT / rate times
// larger, that is 1e7/rate, about 81,215 at a rate of 123.13. It is NOT a flat
// 1e7: that is the constant part with the FX divisor dropped, and a caller that
// sized its own overflow guard on it would size it two orders of magnitude
// wrong. Whether the result still fits the ledger's own bigint is the caller's
// decision to make explicitly. Narrowing here through big.Int.Int64() would return an undefined
// value with no error on overflow, which is the defect tracked in issue #1547.
func BDTSubunitsToCredits(subunits *big.Int, rate *big.Rat) (*big.Int, error) {
	if subunits == nil {
		return nil, fmt.Errorf("payments: bdt subunits must be non-nil")
	}
	if subunits.Sign() < 0 {
		return nil, fmt.Errorf("payments: bdt subunits must not be negative, got %s", subunits)
	}
	if rate == nil || rate.Sign() <= 0 {
		return nil, fmt.Errorf("payments: usd to bdt rate must be positive")
	}
	if subunits.Sign() == 0 {
		return new(big.Int), nil
	}

	exact := new(big.Rat).SetInt(subunits)
	exact.Quo(exact, rate)
	exact.Mul(exact, new(big.Rat).SetFrac64(CreditsPerUSD, SubunitsPerBDT))
	return ratRoundHalfUp(exact), nil
}
