package grants

import (
	"fmt"
	"math/big"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// =============================================================================
// Taka to credits, the one place a grant crosses the unit boundary (#1659).
//
// A grant is denominated in taka. Everything about the request says so: the
// JSON key is amount_bdt_subunits, the column is amount_bdt_subunits, the
// currency column is a CHECK-constrained 'BDT', and the validation messages
// talk about subunits. The ledger it posts to is denominated in credits, at
// payments.CreditsPerUSD (a billion) to the USD. Those are different units and
// the step between them is an exchange rate, never the identity.
//
// Before this file existed, CreateWithLedger did:
//
//	creditsDelta := input.AmountBDTSubunits.Int64()
//
// and wrote that same integer into both columns. Granting 100,000 subunits,
// 1,000 taka, about eight USD, posted 100,000 credits, which is 0.0001 USD of
// inference: short by roughly seven orders of magnitude.
//
// WHICH DENOMINATION IS RIGHT. Issue #1659 leaves this open, because the code
// does not answer it: either a grant is taka and has to be converted, or it is
// really credits and every name around it is lying. It is taka. Four things
// say so and nothing says otherwise: the column name, the JSON key, the
// currency CHECK, and the fact that the product's other admin-facing money
// figures (budget caps, invoices, spend alerts) are all taka too. An admin
// deciding to hand a customer a thousand taka of credit is making a taka-sized
// decision, and the alternative reading would require a migration renaming a
// column on an append-only table to make a lie true.
//
// WHICH RATE. The platform rate: payments.PlatformUSDBDTRate, which is the
// HIVE_USD_BDT_RATE operator override or the documented default. NOT the
// account's own fx_snapshots rate, which is what an invoice uses. An invoice
// has to reconcile against a receipt the customer already holds, so it is
// denominated at the rate that customer bought at. A grant is not a purchase
// and reconciles against nothing: it is the platform giving away its own money
// today, so the platform's rate today is the honest denomination. Reaching for
// the recipient's last purchase rate would also mean a DB round trip inside
// the grant transaction and would denominate a gift at a rate that could be
// months stale, or absent entirely for an account that has never paid.
//
// The rate and its provenance are recorded in the ledger entry's metadata, so
// the row reproduces its own amounts without a schema change: public.
// credit_grants keeps the taka figure, public.credit_ledger_entries keeps the
// credits, and the metadata says which rate joins them.
// =============================================================================

// creditsForGrant converts an admin's taka amount into the credit quantity the
// ledger stores, and reports the rate it used.
//
// Fail closed at every arm. An unusable rate, an unusable amount, or a credit
// quantity beyond the ledger's bigint column all return an error and no value,
// because a grant is an append to an immutable ledger: a wrong row cannot be
// taken back, only compensated by another row.
//
// The int64 comes from an explicit IsInt64 check and never from a bare
// big.Int.Int64(), which returns an undefined value with no error when the
// quantity does not fit. That silent narrowing is the defect tracked in issue
// #1547 and this function does not repeat it. The check matters more here than
// it looks: a subunit amount comfortably inside the credit_grants bigint
// column becomes a credit quantity about ten million times larger, so the
// ledger column, not the grants column, is the binding limit.
func creditsForGrant(subunits *big.Int) (int64, payments.USDBDTRate, error) {
	rate, err := payments.PlatformUSDBDTRate()
	if err != nil {
		return 0, payments.USDBDTRate{}, fmt.Errorf("grants: resolve usd to bdt rate: %w", err)
	}

	credits, err := payments.BDTSubunitsToCredits(subunits, rate.Rate)
	if err != nil {
		return 0, payments.USDBDTRate{}, fmt.Errorf("grants: convert grant amount: %w", err)
	}

	if !credits.IsInt64() {
		return 0, payments.USDBDTRate{}, fmt.Errorf(
			"grants: %s subunits at %s BDT per USD is %s credits, which overflows the credit_ledger_entries.credits_delta bigint column",
			subunits, rate.Display, credits)
	}

	return credits.Int64(), rate, nil
}
