package inference

// Charge derivation for the token-metered endpoints (#688).
//
// Chat completions, legacy completions, the Responses API and embeddings all
// used to settle at one credit per token: settlementCredits returned the
// provider's total_tokens and the settlement calls passed that figure straight
// through as ActualCredits. At 100000 credits per USD that is 10.00 USD per
// million tokens on every alias, about 95 times hive-fast's published input
// price, and it consulted model_aliases nowhere. Every charge now comes from
// the resolved alias's catalog row instead, the same way the audio path has
// derived its charge since PR #671.
//
// The arithmetic itself is NOT reimplemented here: metering.ChargeCredits is
// the single copy in the tree (per million units, math/big, one round half up
// over the summed components, D-031). This file only decides which quantities
// and which prices go into it, and refuses the request when the catalog row
// cannot answer that.

import (
	"log"
	"net/http"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
)

// PriceUnitTokens is the model_aliases.price_unit value every text modality
// carries, and the only unit the endpoints in this package can meter. The
// column is NOT NULL with a CHECK limiting it to tokens, characters or seconds
// (supabase/migrations/20260801_13_alias_price_unit.sql); characters and
// seconds belong to the voice endpoints, which meter their own quantities in
// internal/audio.
const PriceUnitTokens = "tokens"

// CanPriceTokens reports whether the resolved alias's catalog row can price a
// token-metered request: the unit must say tokens explicitly, and at least one
// side must carry a price. An empty unit is not treated as tokens even though
// that is the column default -- an implicit unit is exactly the ambiguity the
// column exists to remove, and control-plane's routing.Service has sent the
// field on every selection since #627.
//
// One price side at zero is legitimate and stays priceable: embeddings meter
// input only, so hive-embedding-default has output_price_credits = 0. Both
// sides at zero is the no-cost-basis case metering.RouteInfo.HasCostBasis
// already names, and routing.Service refuses such an alias outright (#617), so
// reaching it here means the catalog and the gateway disagree.
func CanPriceTokens(route SelectRouteResult) bool {
	return route.PriceUnit == PriceUnitTokens &&
		(route.Pricing.InputPriceCredits > 0 || route.Pricing.OutputPriceCredits > 0)
}

// CreditsForTokens converts metered token counts into whole credits at the
// alias's catalog price, flooring a nonzero quantity at one credit so a request
// that consumed real work is never served free. Identical shape to
// audio.creditsForQuantity, one modality wider: text meters two quantities at
// two prices instead of one.
//
// Token counts come from a provider response, so they are external input on a
// money path: a negative count is clamped away rather than allowed to subtract
// from the charge.
func CreditsForTokens(route SelectRouteResult, inputTokens, outputTokens int64) int64 {
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	credits := metering.ChargeCredits(
		metering.UnitCharge{Quantity: inputTokens, CreditsPerMillion: route.Pricing.InputPriceCredits},
		metering.UnitCharge{Quantity: outputTokens, CreditsPerMillion: route.Pricing.OutputPriceCredits},
	)
	if inputTokens+outputTokens > 0 && credits < 1 {
		credits = 1
	}
	return credits
}

// requireTokenPricing refuses a request whose resolved alias is not priced in
// tokens, before any hold is taken and before the request reaches a provider
// (D-034). Converting a per-second or per-character price into a per-token one
// would invent a rate, and serving the request anyway would serve it free; both
// are worse than a refusal, so this fails closed. Same decision, same shape and
// same customer-facing answer as audio.Handler.requirePriceUnit.
//
// The message names no provider, no amount and no currency: it is the same
// model-unavailable shape the other refusals on this path already use.
func requireTokenPricing(w http.ResponseWriter, route SelectRouteResult, endpoint, model string) bool {
	if CanPriceTokens(route) {
		return true
	}
	log.Printf("inference: refusing endpoint=%s alias=%s: catalog price is %d/%d credits per million %q but this endpoint meters %q",
		endpoint, model, route.Pricing.InputPriceCredits, route.Pricing.OutputPriceCredits, route.PriceUnit, PriceUnitTokens)
	code := "model_not_supported"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"The requested model is not available for this endpoint. Please retry with a supported model.", &code)
	return false
}
