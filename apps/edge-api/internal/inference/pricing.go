package inference

// Charge derivation for the token-metered endpoints (#688).
//
// Chat completions, legacy completions, the Responses API and embeddings all
// used to settle at one credit per token: settlementCredits returned the
// provider's total_tokens and the settlement calls passed that figure straight
// through as ActualCredits. At the pre-rescale unit (100000 credits per
// USD) that is 10.00 USD per
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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

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

// DefaultCacheReadRateNum/Denom and DefaultCacheWriteRateNum/Denom are the
// FALLBACK multipliers applied, relative to a route's own input price, when
// the catalog carries no cache price for an alias that is actually reporting
// cache token usage. They are a safety net for a NEW alias the seeding
// migration has not caught up to yet, never the normal path: a real,
// per-model rate belongs in model_aliases.cache_read_price_credits /
// cache_write_price_credits, because the true multiplier varies by provider
// and model (Anthropic and GPT-5-class: 0.1x read / 1.25x write; OpenAI o3:
// 0.25x / free; gpt-4o and o1: 0.5x / free; Groq: 0.5x / free; DeepSeek:
// ~0.033x (1/30) read / 1.0x write, per DeepSeek's own Models & Pricing page
// (https://api-docs.deepseek.com/quick_start/pricing, cache-hit vs
// cache-miss input columns, fetched 2026-08-25). deepseek-v4-pro's published
// rate is an exact $0.022 / $0.66 = 1/30; deepseek-v4-flash's $0.007 / $0.22
// rounds to the same 1/30 at the page's 3-decimal display precision. This
// comment corrects two earlier, mutually-inconsistent figures: it previously
// said 0.1x here (a copy of the Anthropic/GPT-5-class row above it, not
// actually sourced from DeepSeek), and vault
// spec-2026-08-25-cache-aware-billing.md separately said ~0.2x. Both were
// wrong. See supabase/migrations/20260825_02_deepseek_cache_read_price_correction.sql
// for the full reconciliation and issue #1176 for the investigation. Falling back
// to the flat input rate instead (treating cache read as ordinary input) is
// exactly the bug this feature fixes, and falling back to zero is revenue
// loss D-034 forbids, so this fallback is the third option, not either of
// those two.
//
// An explicit stored value of zero is NOT "unpopulated": model_aliases rows
// that deliberately record a model as having no published cache rate (e.g.
// hive-small's Groq route, 20260822_02_catalog_alias_restructure.sql) use an
// explicit 0, and that is a real, considered price, honoured as-is with no
// fallback and no WARN -- the same "one priced side may legitimately be
// zero" rule CanPriceTokens already applies to the input/output columns.
// Only a NULL pointer (never configured at all) triggers the fallback.
const (
	DefaultCacheReadRateNum    = 1
	DefaultCacheReadRateDenom  = 10
	DefaultCacheWriteRateNum   = 5
	DefaultCacheWriteRateDenom = 4
)

// resolveCacheRate prices one cache component (side is "read" or "write") as
// a metering.UnitCharge: the alias's own catalog rate when the catalog
// carries one (including a deliberate zero), or the documented fallback
// multiplier of inputRate when it does not.
//
// The fallback is carried as an EXACT FRACTION (numerator folded into
// Quantity, denominator into RateDivisor) instead of being pre-scaled into a
// whole credits-per-million integer: 1/10x of an input rate below 5 credits
// per million is a fraction of one credit per million, and pre-rounding it
// to a whole number would collapse it to zero, dropping a request full of
// cache-read tokens all the way down to the 1-credit floor no matter how
// many tokens it consumed (PR #1157 review finding; regression test
// TestCreditsForTokensFractionalFallbackSurvivesChargeArithmetic).
//
// It logs a WARN and increments cacheBillingFallbackRateUsed, naming the
// alias and which side fell back, only when quantity is actually nonzero: a
// route that has never once seen a cache token should not spam a WARN (or a
// counter increment) every request just because its cache columns happen to
// be unset.
func resolveCacheRate(priceCredits *int64, inputRate, numerator, denominator, quantity int64, aliasID, provider, side string) metering.UnitCharge {
	if priceCredits != nil {
		return metering.UnitCharge{Quantity: quantity, CreditsPerMillion: *priceCredits}
	}
	if quantity > 0 {
		log.Printf("inference: WARN falling back to default cache-%s rate %d/%d x input_rate=%d alias=%s provider=%s tokens=%d: "+
			"catalog has no %s cache price for this alias, see the seeding migration and model_aliases.cache_%s_price_credits",
			side, numerator, denominator, inputRate, aliasID, provider, quantity, side, side)
		cacheBillingFallbackRateUsed.WithLabelValues(aliasID, provider, side).Inc()
	}
	return metering.UnitCharge{
		Quantity:          quantity * numerator,
		CreditsPerMillion: inputRate,
		RateDivisor:       denominator,
	}
}

// assertCacheBillingMagnitude is the runtime half of the cache-billing
// contract's self-check (vault spec-2026-08-25-cache-aware-billing.md
// section 5): a semantics inversion in NormalizeCacheUsage (adding where the
// inclusive shape should have subtracted, or the reverse) does not produce a
// small error, it roughly doubles or nearly-frees the charge. This catches
// that CLASS of bug in production, not just in a table test, by comparing the
// real charge against twice the highest-rate bound: two times what pricing
// every prompt token at the HIGHEST of the input, cache-read and cache-write
// rates would have produced. It never blocks, alters, or refuses the charge: a
// ceiling breach is loud evidence to investigate, not grounds to fail a
// request that has already been served (D-034 is about never charging zero
// for delivered work, not about refusing a suspicious charge after the fact).
func assertCacheBillingMagnitude(route SelectRouteResult, fresh, cacheRead, cacheWrite, output, credits int64) {
	totalPrompt := fresh + cacheRead + cacheWrite
	// The ceiling scales off the HIGHEST of the three input-side rates, not
	// the flat input rate alone: a catalog row whose cache rate is priced
	// above the input rate (a real, sourced case -- Anthropic's 1h cache
	// write TTL reaches 2.0x) would otherwise make this guard false-positive
	// on a perfectly correct charge, degrading a real signal into noise on
	// exactly the rows that need it working. Using the max of all three
	// keeps the guard a true upper bound under any catalog configuration,
	// not just the ones seeded today.
	inputSideRate := route.Pricing.InputCredits()
	if r := derefPrice(route.Pricing.CacheReadPriceCredits); r > inputSideRate {
		inputSideRate = r
	}
	if r := derefPrice(route.Pricing.CacheWritePriceCredits); r > inputSideRate {
		inputSideRate = r
	}
	ceiling := metering.ChargeCredits(
		metering.UnitCharge{Quantity: totalPrompt * 2, CreditsPerMillion: inputSideRate},
		metering.UnitCharge{Quantity: output, CreditsPerMillion: route.Pricing.OutputCredits()},
	)
	// The same 1-credit floor CreditsForTokens applies to a nonzero-but-cheap
	// request also applies here: without it, a tiny request (e.g. a single
	// output token on a low per-million rate) has a raw proportional ceiling
	// of 0, and the floor alone would trip this guard on every such request,
	// exactly the noisy-false-positive shape that gets a real WARN ignored.
	if totalPrompt+output > 0 && ceiling < 1 {
		ceiling = 1
	}
	if credits > ceiling {
		log.Printf("inference: BUG: cache billing magnitude guard tripped alias=%s provider=%s credits=%d ceiling=%d fresh=%d cache_read=%d cache_write=%d output=%d: "+
			"charge exceeds 2x the highest-of-rates bound, which usually means a cache semantics inversion",
			route.AliasID, route.Provider, credits, ceiling, fresh, cacheRead, cacheWrite, output)
		cacheBillingMagnitudeGuardTrips.WithLabelValues(route.AliasID, route.Provider).Inc()
	}
}

// CreditsForTokens converts metered token counts into whole credits at the
// alias's catalog price, pricing fresh input, cache-read input, cache-write
// input and output at four independent rates rather than one flat input
// rate. It floors a nonzero total quantity at one credit so a request that
// consumed real work is never served free (D-034).
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
func CreditsForTokens(route SelectRouteResult, freshInputTokens, cacheReadTokens, cacheWriteTokens, outputTokens int64) int64 {
	if route.Pricing.IsUpstreamActual() {
		// Not a panic. The reachable chain is settleStream, called from a defer
		// in executeStreaming, so a panic here would fire during deferred
		// unwinding, before FinalizeReservation and before ReleaseReservation.
		// net/http recovers it and the process survives, but the reservation
		// reaches no terminal state at all and the hold is stranded, which is
		// the outcome this guard was written to avoid. Returning the hold-sized
		// sentinel keeps the settlement path intact and loud instead.
		log.Printf("inference: BUG: CreditsForTokens called for an upstream_actual route alias=%s; "+
			"its charge must come from the reported upstream cost, not the catalog price columns. "+
			"Returning zero so the caller's own not-billable handling releases the hold rather than charging a fiction.",
			route.AliasID)
		return 0
	}
	// Loud, not silent: matches the clamp-and-alarm shape NormalizeCacheUsage
	// already uses one frame up. A negative count reaching this far means
	// either a caller that bypassed NormalizeCacheUsage, or a corrupted
	// component NormalizeCacheUsage's own boundary clamp did not fully
	// absorb; either way it is worth a trace, not a silent zero (review
	// finding on PR #1157).
	freshInputTokens = clampNonNegative(freshInputTokens, route.AliasID, route.Provider, "fresh_input_tokens")
	cacheReadTokens = clampNonNegative(cacheReadTokens, route.AliasID, route.Provider, "cache_read_tokens")
	cacheWriteTokens = clampNonNegative(cacheWriteTokens, route.AliasID, route.Provider, "cache_write_tokens")
	outputTokens = clampNonNegative(outputTokens, route.AliasID, route.Provider, "output_tokens")

	inputRate := route.Pricing.InputCredits()
	credits := metering.ChargeCredits(
		metering.UnitCharge{Quantity: freshInputTokens, CreditsPerMillion: inputRate},
		resolveCacheRate(route.Pricing.CacheReadPriceCredits, inputRate,
			DefaultCacheReadRateNum, DefaultCacheReadRateDenom, cacheReadTokens, route.AliasID, route.Provider, "read"),
		resolveCacheRate(route.Pricing.CacheWritePriceCredits, inputRate,
			DefaultCacheWriteRateNum, DefaultCacheWriteRateDenom, cacheWriteTokens, route.AliasID, route.Provider, "write"),
		metering.UnitCharge{Quantity: outputTokens, CreditsPerMillion: route.Pricing.OutputCredits()},
	)
	if freshInputTokens+cacheReadTokens+cacheWriteTokens+outputTokens > 0 && credits < 1 {
		credits = 1
	}
	assertCacheBillingMagnitude(route, freshInputTokens, cacheReadTokens, cacheWriteTokens, outputTokens, credits)
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
func ChatSettlementCredits(route SelectRouteResult, hasUsage bool, freshInputTokens, cacheReadTokens, cacheWriteTokens, outputTokens int64,
	requestBody []byte, content string) (credits int64, confirmed bool, delivered bool) {
	return settlementCredits(route, hasUsage, freshInputTokens, cacheReadTokens, cacheWriteTokens, outputTokens,
		promptText(EndpointChatCompletions, requestBody), content)
}

// Request bounds for a variable-price alias.
//
// These exist because provider.max_price bounds the RATE and nothing else. It
// filters out endpoints above a per-million ceiling; it does not bound how many
// tokens one request may contain. openrouter/auto-beta advertises a 2,000,000
// token context, so at the configured ceiling a single request near that
// context could cost roughly 6.00 USD of prompt alone, which is 840,000 credits
// after margin, against a 200,000 credit hold. Settlement charges the reported
// cost rather than the hold, so without a bound on the REQUEST one call could
// settle several times past the solvency gate the hold is supposed to be.
//
// Bounding both sides makes the hold a proof rather than a hope:
//
//	prompt:     262,144 bytes is a rigorous upper bound of 262,144 tokens,
//	            because a token can never be fewer than one UTF-8 byte, and the
//	            body also carries JSON structure that is not prompt text.
//	            262,144 x 3.00 USD/M x 1.4 = 1.10 USD = 110,096 credits.
//	completion: 16,384 tokens x 15.00 USD/M x 1.4 = 0.35 USD = 34,406 credits.
//	total:      about 144,502 credits, comfortably inside the 200,000 hold.
//
// Change either constant, or provider.max_price in deploy/litellm/config.yaml,
// and reservation_estimate_credits in the migration has to be re-derived. The
// three numbers are one decision, not three.
const (
	// VariablePriceMaxRequestBytes caps the request body for a variable-price
	// alias. 256 KiB of chat is enormous; the cap exists to bound spend, not to
	// be reached.
	VariablePriceMaxRequestBytes = 256 * 1024
	// VariablePriceMaxCompletionTokens caps generated output for a
	// variable-price alias, forced onto the outbound request so a client
	// cannot raise it.
	VariablePriceMaxCompletionTokens = 16384
)

// completionLimitFields names the request field that caps generated tokens for
// each endpoint. Chat carries both spellings and OpenAI treats the newer one as
// authoritative, so both are pinned rather than guessing which the upstream
// honours.
var completionLimitFields = map[string][]string{
	EndpointChatCompletions: {"max_tokens", "max_completion_tokens"},
	EndpointCompletions:     {"max_tokens"},
	EndpointResponses:       {"max_output_tokens"},
}

// EnforceVariablePriceBounds refuses an over-large request and pins the
// completion ceiling for a variable-price alias, returning the body to
// dispatch. For every other alias it is a pass-through and costs one comparison.
//
// It returns ok=false only when it has already written the customer-facing
// refusal.
func EnforceVariablePriceBounds(w http.ResponseWriter, route SelectRouteResult, endpoint, model string, body []byte) ([]byte, bool) {
	if !route.Pricing.IsUpstreamActual() {
		return body, true
	}

	if len(body) > VariablePriceMaxRequestBytes {
		log.Printf("inference: refusing oversize request on a variable-price alias endpoint=%s alias=%s bytes=%d limit=%d: the credit hold is only provably sufficient below this size",
			endpoint, model, len(body), VariablePriceMaxRequestBytes)
		code := "context_length_exceeded"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			"This request is too large for the requested model. Please shorten the input and try again.", &code)
		return nil, false
	}

	bounded, err := clampCompletionLimit(body, completionLimitFields[endpoint])
	if err != nil {
		// Refuse rather than dispatch unbounded. An unparseable body here is
		// not something to shrug at on a path where the ceiling is what keeps
		// the charge inside the hold.
		log.Printf("inference: refusing variable-price request whose body could not be bounded endpoint=%s alias=%s: %v", endpoint, model, err)
		code := "invalid_request_error"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			"The request body could not be processed. Please check the request and try again.", &code)
		return nil, false
	}
	return bounded, true
}

// clampCompletionLimit forces each named field down to
// VariablePriceMaxCompletionTokens, setting it when the client omitted it. Every
// other field the caller sent survives byte for byte, the same contract
// metering.RewriteBody keeps.
func clampCompletionLimit(raw []byte, fields []string) ([]byte, error) {
	if len(fields) == 0 {
		return raw, nil
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("inference: decode body to bound completion: %w", err)
	}
	if decoded == nil {
		decoded = map[string]json.RawMessage{}
	}

	capped := json.RawMessage(strconv.Itoa(VariablePriceMaxCompletionTokens))
	for _, field := range fields {
		current, present := decoded[field]
		if present {
			var value int64
			// A field we cannot read is replaced rather than trusted: leaving
			// an unreadable ceiling in place would defeat the bound.
			if err := json.Unmarshal(current, &value); err == nil && value <= VariablePriceMaxCompletionTokens && value > 0 {
				continue
			}
		}
		decoded[field] = capped
	}

	return json.Marshal(decoded)
}

// Flat per-endpoint pre-dispatch credit holds, in the current credit unit
// (1 USD = 1e9 credits since the 2026-08-23 rescale; migration
// 20260823_40_credit_unit_rescale_billion.sql). These are authorization
// floors picked before dispatch, never charges; settlement replaces them with
// the catalog price of what was actually metered.
const (
	// DefaultHoldText is the hold taken by /v1/chat/completions,
	// /v1/completions and /v1/responses: $0.10 equivalent.
	DefaultHoldText int64 = 100_000_000
	// DefaultHoldEmbeddings is the hold taken by /v1/embeddings:
	// $0.01 equivalent.
	DefaultHoldEmbeddings int64 = 10_000_000
)

// ReservationCredits sizes the up-front credit hold for a request.
//
// For an ordinary fixed-price alias this is unchanged: the flat per-endpoint
// figure the caller already passed (DefaultHoldText for chat, completions and
// responses, DefaultHoldEmbeddings for embeddings), scaled to the current
// credit unit.
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
type VariableSettlement struct {
	Credits   int64
	Confirmed bool
	Delivered bool
	// Reason is a short machine-ish token for the log line, never
	// customer-facing.
	Reason string
	// GenerationID is the upstream's own id for this generation. It is the
	// audit handle: LiteLLM rewrites the response `model` field, so this id is
	// what lets anyone recover WHICH model the router actually chose long after
	// the fact. Logged with every settlement, and deliberately kept out of the
	// customer response and out of audit_log, which fans out to third parties.
	GenerationID string
}

func UpstreamActualSettlement(rawUsage []byte, heldCredits int64, hasUsage bool,
	inputTokens, outputTokens int64, content string) VariableSettlement {

	tokensSeen := hasUsage && inputTokens+outputTokens > 0
	if !tokensSeen && content == "" {
		return VariableSettlement{Reason: "nothing_delivered"}
	}

	// A hold of zero reaches here in exactly one case: the Enterprise posture,
	// which has no reservation and charges nothing. Flooring it to 1 would
	// invent a one-credit figure for the trace row on the one alias whose cost
	// is genuinely unknown, so the floor is applied only where a real hold
	// exists. Where there is none, the fail-closed branch returns zero credits
	// and NOT delivered, so the caller releases rather than charging; that is
	// the one zero this function is allowed to produce and it never reaches a
	// ledger.
	if heldCredits < 0 {
		heldCredits = 0
	}

	charge, err := ParseUpstreamCost(rawUsage)
	if err != nil {
		reason := upstreamCostFailureReason(err)
		if heldCredits == 0 {
			// Reachable only where there is no reservation at all, which today
			// means the Enterprise posture, where nothing is charged. Naming it
			// keeps it out of the "we charged the hold" bucket and makes a
			// future regression in the hold plumbing visible instead of
			// arriving as a silent zero, which is exactly how the
			// reserved_credits key mismatch presented.
			reason = "no_hold_to_charge:" + reason
		}
		return VariableSettlement{
			Credits: heldCredits, Delivered: true,
			Reason: reason, GenerationID: charge.GenerationID,
		}
	}

	credits, err := CreditsForUpstreamCost(charge.CostUSD)
	if err != nil {
		return VariableSettlement{
			Credits: heldCredits, Delivered: true,
			Reason: upstreamCostFailureReason(err), GenerationID: charge.GenerationID,
		}
	}
	return VariableSettlement{
		Credits: credits, Confirmed: true, Delivered: true,
		Reason: "upstream_cost", GenerationID: charge.GenerationID,
	}
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
	case errors.Is(err, ErrUpstreamCostImplausible):
		return "upstream_cost_implausible"
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
	// Accessors, not the raw fields: those are *int64 now, and fmt accepts %d
	// for a pointer and prints the ADDRESS. go vet does not flag it because %d
	// is a legal verb for a pointer, so this would have quietly turned the only
	// operator-facing explanation of a refusal into a memory address.
	log.Printf("inference: refusing endpoint=%s alias=%s: catalog price is %d/%d credits per million %q but this endpoint meters %q",
		endpoint, model, route.Pricing.InputCredits(), route.Pricing.OutputCredits(), route.PriceUnit, PriceUnitTokens)
	code := "model_not_supported"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"The requested model is not available for this endpoint. Please retry with a supported model.", &code)
	return false
}
