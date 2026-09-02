package payments

import (
	"fmt"
	"math/big"
)

// =============================================================================
// What a credit purchase costs (D-065, D-066; issue #1693)
//
// Margin is taken here, once, at the point of sale. It used to be taken on the
// inference path instead, as a 1.4 multiplier on every burn, and D-064 retired
// that on 2026-09-02. Nothing downstream of a purchase may multiply again: a
// burn now costs exactly what the provider charged, converted at the peg.
//
// THE ORDER OF OPERATIONS IS FIXED, and it is the whole contract of this file:
//
//	1. credits            the quantity the buyer asked for, an integer, already
//	                      validated as a whole multiple of CreditIncrement.
//	2. USD at the peg     credits / CreditIncrement, kept as an EXACT rational
//	                      number of US cents. The D-046 peg (1 USD = 1e9
//	                      credits) is untouched by any of this: it decides how
//	                      many credits are granted, never what they cost.
//	3. markup             times (1 + PurchaseMarkupRate), so 6 percent.
//	4. FX                 for a local-currency payer only, times the effective
//	                      rate, which already carries FXFeeRate folded inside it
//	                      (fx.go). A USD payer never reaches this step, which is
//	                      what stops the FX markup reaching someone who is not
//	                      converting currency.
//	5. round              ONCE per currency, at that currency's minor unit.
//
// Steps 2 to 4 are one unbroken big.Rat. No float64 appears anywhere in this
// file, and no intermediate is rounded: the exact marked-up cent figure is what
// step 4 multiplies, so a BDT price is never the FX conversion of an
// already-rounded USD price.
//
// ROUNDING DIRECTION AND UNIT, stated because both the markup and the FX
// conversion produce fractions:
//
//	direction  truncate toward zero, so a fraction of a minor unit is dropped
//	           rather than charged. That is the payer's favour, and it is the
//	           direction the BDT conversion has always used
//	           (usdCentsToLocalPaisa, issue #114). One rule for both currencies
//	           rather than two, so nobody has to remember which side is which.
//	unit       US cents for a USD price, paisa for a BDT price. Both are the
//	           currency's own minor unit, which is what the rails charge in.
//
// Truncation can never take a price BELOW the un-marked-up one: `credits` is a
// whole multiple of CreditIncrement, so step 2 is an exact integer, and
// multiplying an integer by 1.06 and flooring cannot land under it.
//
// REPRODUCIBILITY. Everything this file used is recorded on the payment intent:
// the credits and the resolved amounts on their own columns, the markup rate in
// metadata (see service.go), and the FX half on the fx_snapshots row the
// intent's fx_snapshot_id points at, which carries the mid rate, the fee rate
// and the effective rate that was actually applied. Issue #1682 exists because
// a stored money figure could not be reproduced from what was stored beside it.
// =============================================================================

// PurchaseMarkupAppliesToLocalCurrency decides whether a local-currency payer
// carries the 6 percent credit markup IN ADDITION TO the FX markup already
// folded into their rate.
//
// True, which is the owner's working assumption recorded in issue #1693: the
// 6 percent applies to every purchase and the 2.5 percent FX markup is
// additional, so a BD customer buying 1e9 credits pays 1.06 USD converted at
// mid x 1.025.
//
// The alternative reading of the same ruling is that the FX markup REPLACES the
// credit markup for a local-currency payer. It is one line: set this to false
// and a BDT price becomes the un-marked-up USD price converted at the marked-up
// rate. The choice is confirmed at the owner's pre-demo review, which is why it
// is a named constant rather than an assumption spread across two call sites.
const PurchaseMarkupAppliesToLocalCurrency = true

// PurchaseAmounts is what a quantity of credits costs, in every currency the
// purchase touches.
type PurchaseAmounts struct {
	// USDCents is the price in US cents, always populated. It is what a USD
	// rail charges, and internal accounting for a local-currency purchase.
	USDCents int64

	// LocalMinor is the price in the local currency's minor unit (paisa for
	// BDT). Zero for a USD purchase, which has no local currency and no FX
	// conversion.
	LocalMinor int64

	// MarkupRate is the credit markup that was actually applied, as a decimal
	// string, so the caller can record it on the row it writes rather than
	// leaving a later reader to assume today's constant applied to a row
	// written months ago.
	MarkupRate string
}

// PriceForCredits prices `credits` at the current markup, and converts to the
// local currency when effectiveRate is non-empty.
//
// effectiveRate is the FX snapshot's effective rate (mid x (1 + FXFeeRate)) as
// a decimal string. Empty means a USD purchase: no conversion happens and no FX
// markup is applied, which is the property TestUSDPurchaseNeverTakesTheFXMarkup
// pins.
func PriceForCredits(credits int64, effectiveRate string) (PurchaseAmounts, error) {
	if credits < 0 {
		return PurchaseAmounts{}, fmt.Errorf("payments: cannot price %d credits", credits)
	}

	// Steps 2 and 3: exact US cents at the peg, then the markup.
	marked := new(big.Rat).SetFrac(big.NewInt(credits), big.NewInt(CreditIncrement))
	local := effectiveRate != ""
	if !local || PurchaseMarkupAppliesToLocalCurrency {
		multiplier, err := markupMultiplier(PurchaseMarkupRate)
		if err != nil {
			return PurchaseAmounts{}, err
		}
		marked.Mul(marked, multiplier)
	}

	amounts := PurchaseAmounts{MarkupRate: PurchaseMarkupRate}

	// Step 5 for USD.
	usdCents, err := truncateToMinorUnit(marked)
	if err != nil {
		return PurchaseAmounts{}, fmt.Errorf("payments: usd price: %w", err)
	}
	amounts.USDCents = usdCents

	if !local {
		return amounts, nil
	}

	// Step 4 then step 5 for the local currency, from the SAME exact rational
	// the USD figure was truncated from rather than from the truncated figure.
	localMinor, err := usdCentsToLocalPaisa(marked, effectiveRate)
	if err != nil {
		return PurchaseAmounts{}, err
	}
	amounts.LocalMinor = localMinor
	return amounts, nil
}

// markupMultiplier turns a markup rate such as "0.06" into the exact rational
// 1.06. A rate that will not parse is an error rather than a silent 1.0: a
// markup that quietly vanishes is the failure mode that has to be loud.
func markupMultiplier(rate string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(rate)
	if !ok {
		return nil, fmt.Errorf("payments: invalid markup rate %q", rate)
	}
	if r.Sign() < 0 {
		return nil, fmt.Errorf("payments: negative markup rate %q", rate)
	}
	return r.Add(r, new(big.Rat).SetInt64(1)), nil
}

// truncateToMinorUnit applies step 5: truncate toward zero to a whole minor
// unit. Range-checked before the int64 conversion, because big.Int.Int64 on an
// oversized value returns the low 64 bits with the sign reinterpreted, which
// would turn an absurd price into a plausible small one.
func truncateToMinorUnit(amount *big.Rat) (int64, error) {
	whole := new(big.Int).Quo(amount.Num(), amount.Denom())
	if !whole.IsInt64() {
		return 0, fmt.Errorf("amount %s does not fit in an int64", whole)
	}
	return whole.Int64(), nil
}
