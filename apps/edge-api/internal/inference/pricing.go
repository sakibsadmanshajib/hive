package inference

// Charge derivation for the token-metered endpoints (#688).
//
// Chat completions, legacy completions, the Responses API and embeddings all
// used to settle at one credit per token: settlementCredits returned the
// provider's total_tokens and the settlement calls passed that figure straight
// through as ActualCredits. At 100000 credits per USD that is 10.00 USD per
// million tokens on every alias, more than two orders of magnitude above
// hive-fast's published input price, and it consulted model_aliases nowhere. Every charge now comes from
// the resolved alias's catalog row instead, the same way the audio path has
// derived its charge since PR #671.
//
// The arithmetic itself is NOT reimplemented here: metering.ChargeCredits is
// the single copy in the tree (per million units, math/big, one round half up
// over the summed components, D-031). This file only decides which quantities
// and which prices go into it, and refuses the request when the catalog row
// cannot answer that.

import (
	"errors"
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
//
// An upstream_actual alias passes on the strength of its MODE, not its price
// columns, which are deliberately NULL: its charge comes from the cost the
// upstream reports for that generation. Control-plane has already refused to
// select such an alias unless it also carries a positive reservation estimate
// (routing.Service), so reaching here in that mode means the hold is fundable.
func CanPriceTokens(route SelectRouteResult) bool {
	if route.PriceUnit != PriceUnitTokens {
		return false
	}
	if route.Pricing.IsUpstreamActual() {
		return true
	}
	return route.Pricing.InputCredits() > 0 || route.Pricing.OutputCredits() > 0
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
//
// Never call this for an upstream_actual route. Its price columns are NULL, so
// this would price every token at zero and hand back the 1-credit floor for a
// request that may have cost dollars. The guard below makes that a loud
// programming error instead of a silent near-free settlement; callers are
// expected to branch on IsUpstreamActual before they get here.
func CreditsForTokens(route SelectRouteResult, inputTokens, outputTokens int64) int64 {
	if route.Pricing.IsUpstreamActual() {
		panic("inference: CreditsForTokens called for an upstream_actual route; " +
			"its charge must come from the upstream reported cost, not the catalog price columns")
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	credits := metering.ChargeCredits(
		metering.UnitCharge{Quantity: inputTokens, CreditsPerMillion: route.Pricing.InputCredits()},
		metering.UnitCharge{Quantity: outputTokens, CreditsPerMillion: route.Pricing.OutputCredits()},
	)
	if inputTokens+outputTokens > 0 && credits < 1 {
		credits = 1
	}
	return credits
}

// ChatSettlementCredits is the session chat path's entry point into the same
// settlement arithmetic the API-key streaming path uses (settlementCredits in
// stream.go), so the two surfaces cannot price identical usage differently.
// It exists rather than exporting the pieces because promptText's endpoint
// argument and the raw request body are exactly the two details a second
// caller could get wrong: the raw bytes carry field names, sampling params and
// tool schemas, and counting those as tokens is issue #602's overcharge.
//
// credits is derived from route's catalog row, never from a token total and
// never from the flat hold. confirmed is true only when the upstream itself
// reported token counts. delivered is false only when nothing was produced at
// all, in which case the caller must release its hold in full rather than
// charge anything.
func ChatSettlementCredits(route SelectRouteResult, hasUsage bool, inputTokens, outputTokens int64,
	requestBody []byte, content string) (credits int64, confirmed bool, delivered bool) {
	return settlementCredits(route, hasUsage, inputTokens, outputTokens,
		promptText(EndpointChatCompletions, requestBody), content)
}

// ReservationCredits sizes the up-front credit hold for a request.
//
// For an ordinary fixed-price alias this is unchanged: the flat per-endpoint
// figure the caller already passed (10000 for chat, completions and responses,
// 1000 for embeddings).
//
// A variable-price alias cannot use that. Its cost is bounded by the
// provider-side price ceiling configured on the route, not by a catalog price,
// and a router can resolve to a model an order of magnitude dearer than the
// flat default assumes. Its hold therefore comes from the catalog row, which
// records both the number and the arithmetic behind it. The endpoint default
// acts as a floor so this can only ever raise a hold, never lower one.
//
// The hold is an authorization, not a price. Settlement releases it in full and
// posts the real cost as the charge, so an oversized hold costs the customer
// nothing beyond briefly reserving headroom.
func ReservationCredits(route SelectRouteResult, endpointDefault int64) int64 {
	estimate := route.Pricing.ReservationEstimateCredits
	if estimate == nil || *estimate <= endpointDefault {
		return endpointDefault
	}
	return *estimate
}

// UpstreamActualSettlement decides what to charge for a request against a
// variable-price alias, where there is no catalog price and the only truth is
// the cost the upstream reported for that generation.
//
// rawUsage is the VERBATIM upstream body (sync) or the verbatim terminal SSE
// chunk (streaming). It is the raw bytes on purpose: our typed structs drop the
// cost field, which is what stops it reaching the customer.
//
// heldCredits is the size of the credit hold already taken for this request.
//
// The contract, and the reason this function exists rather than a few inline
// branches at two call sites:
//
//   - delivered false means nothing was produced. The caller releases the hold
//     in full and charges nothing. This is the ONLY path that charges zero, and
//     it is reached only when no tokens and no content exist.
//   - a usable reported cost settles at that cost times margin, confirmed.
//   - EVERY other outcome, absent cost, unparseable cost, negative cost, a
//     confident zero alongside real tokens, settles at the FULL HOLD flagged
//     unconfirmed. Not zero. A failed cost lookup is the one thing that must
//     never become a free request (D-034), and the hold is a figure the account
//     was already checked against, so charging it can neither overdraw nor
//     silently give the work away. Unconfirmed routes it into the existing
//     reconciliation state rather than recording it as measured truth.
//
// reason is a short machine-ish token for the log line, never customer-facing.
func UpstreamActualSettlement(rawUsage []byte, heldCredits int64, hasUsage bool,
	inputTokens, outputTokens int64, content string) (credits int64, confirmed bool, delivered bool, reason string) {

	tokensSeen := hasUsage && inputTokens+outputTokens > 0
	if !tokensSeen && content == "" {
		return 0, false, false, "nothing_delivered"
	}

	// A hold of zero would make the fail-closed branch below settle at zero,
	// which is the exact outcome it exists to prevent. Callers only reach here
	// with a live reservation, so this is a guard against a future caller
	// rather than a case seen today.
	if heldCredits < 1 {
		heldCredits = 1
	}

	charge, err := ParseUpstreamCost(rawUsage)
	if err != nil {
		return heldCredits, false, true, upstreamCostFailureReason(err)
	}

	credits, err = CreditsForUpstreamCost(charge.CostUSD)
	if err != nil {
		return heldCredits, false, true, upstreamCostFailureReason(err)
	}
	return credits, true, true, "upstream_cost"
}

// upstreamCostFailureReason maps a cost-read failure to a distinct log token.
// The cases are kept apart rather than folded into one "cost_unavailable"
// because "the field was missing" and "the field said zero" have different
// causes and different fixes, and telling them apart in a log is the whole
// point of them being separate errors.
func upstreamCostFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrUpstreamCostAbsent):
		return "upstream_cost_absent"
	case errors.Is(err, ErrUpstreamCostZero):
		return "upstream_cost_zero"
	case errors.Is(err, ErrUpstreamCostNegative):
		return "upstream_cost_negative"
	case errors.Is(err, ErrUpstreamCostUnparseable):
		return "upstream_cost_unparseable"
	default:
		return "upstream_cost_error"
	}
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
