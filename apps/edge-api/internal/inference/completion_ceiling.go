package inference

import (
	"encoding/json"
	"log"
)

// Caller completion ceilings (issue #1283).
//
// THE INVARIANT
//
//	A request that specifies max_tokens: N must never be billed for more than
//	N completion tokens.
//
// It is enforced at two boundaries, and both are load-bearing:
//
//  1. REQUEST BOUNDARY. The ceiling the caller set reaches the provider
//     unchanged, so the response they receive is the size they asked for. This
//     is the only boundary that can stop a caller receiving 1650 tokens they
//     capped at 8, and it is what makes the reported usage honest rather than
//     merely cheap.
//
//  2. SETTLEMENT BOUNDARY, here. Whatever the provider reports, the metered
//     completion count is capped at the caller's own ceiling before anything
//     reads it: the ledger charge, the usage rollup, and the usage block the
//     caller receives. The money guarantee must not rest on every upstream
//     honouring a parameter we merely forwarded, on no future rewrite of the
//     outbound body, and on no provider counting hidden reasoning into
//     completion_tokens. Those are all hopes; this is a bound.
//
// Live evidence for needing the second boundary even with the first correct:
// PR #1225 shipped a pre-dispatch rewrite that inflated the caller's ceiling by
// a per-pool reasoning reserve, on the premise that hidden reasoning would burn
// the reserve while visible content stayed inside the caller's budget. Nothing
// upstream separates the two, so the model spent the inflated ceiling on
// whichever it emitted, and max_tokens: 8 was billed for 1650 completion
// tokens. A rewrite that only had to be wrong once is exactly what a settlement
// bound exists to survive.
//
// NOT covered here, stated rather than left to be discovered: a variable-price
// alias (Pricing.IsUpstreamActual) settles on the cost the upstream reported
// for the generation, not on a token count times a catalog rate, so there is no
// per-token figure to cap. Its spend is bounded instead by
// EnforceVariablePriceBounds, which forces the outbound completion ceiling down
// to VariablePriceMaxCompletionTokens, and by the hold clamp in control-plane's
// finalizeLocked.

// requestedCompletionCeiling reports the completion-token ceiling the CALLER
// set on this request, or 0 when they set none.
//
// It must be read from the caller's ORIGINAL body, before
// EnforceVariablePriceBounds (which forces a ceiling Hive chose, not one the
// caller asked for) and before any other outbound rewrite. Reading it later
// would return Hive's own number and the clamp would guarantee nothing.
//
// It reads completionLimitFields, so each endpoint contributes exactly the
// ceiling fields that endpoint speaks: chat both spellings, legacy completions
// only max_tokens, responses only max_output_tokens, embeddings none.
//
// With both chat spellings present it takes the SMALLER. The request is
// self-contradictory and no reading of it is provably what the caller meant, so
// the tie goes to the number that keeps the stated invariant literally true for
// whichever field they wrote: a caller who typed max_tokens: 8 anywhere in the
// body is never billed for more than 8. That under-charges Hive on a malformed
// request, which is the direction every estimate on this path already errs in.
//
// A field that is absent, null, non-positive or unparseable contributes
// nothing: a caller who set no usable ceiling declared no budget for this
// gateway to hold them to.
func requestedCompletionCeiling(endpoint string, body []byte) int64 {
	fields := completionLimitFields[endpoint]
	if len(fields) == 0 || len(body) == 0 {
		return 0
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil || decoded == nil {
		return 0
	}
	var ceiling int64
	for _, field := range fields {
		raw, present := decoded[field]
		if !present {
			continue
		}
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil || value <= 0 {
			continue
		}
		if ceiling == 0 || value < ceiling {
			ceiling = value
		}
	}
	return ceiling
}

// clampUsageToCeiling caps usage.CompletionTokens at the caller's ceiling and
// recomputes total_tokens, reporting whether it changed anything.
//
// It mutates the ONE usage object that both the customer response and the
// ledger charge are derived from, which is the point: a clamp applied to the
// charge alone would leave the caller reading a completion count they were
// never billed for, and every OpenAI-compatible cost dashboard reads that
// field. One number, one place, so the two can never disagree.
//
// reasoning_tokens is capped alongside it, because a breakdown claiming more
// reasoning tokens than the completion total it breaks down is not a payload
// any client should have to reconcile.
//
// Every clamp is logged. An upstream routinely overrunning a ceiling it was
// sent is a provider fault worth seeing, and after this fix it is also the
// signal that something re-inflated the outbound body again.
func clampUsageToCeiling(usage *UsageResponse, ceiling int64, endpoint, aliasID string) bool {
	if usage == nil || ceiling <= 0 || usage.CompletionTokens <= ceiling {
		return false
	}
	log.Printf("inference: completion ceiling clamp engaged endpoint=%s alias=%s requested_max_tokens=%d upstream_completion_tokens=%d billed_completion_tokens=%d",
		endpoint, aliasID, ceiling, usage.CompletionTokens, ceiling)
	usage.CompletionTokens = ceiling
	usage.TotalTokens = usage.PromptTokens + ceiling
	if usage.CompletionTokensDetails != nil && usage.CompletionTokensDetails.ReasoningTokens > ceiling {
		usage.CompletionTokensDetails.ReasoningTokens = ceiling
	}
	return true
}

// rewriteNormalizedUsage replaces the "usage" member of an already-normalized
// response body with the clamped figure, so the bytes the caller receives carry
// the same number the ledger recorded.
//
// It re-serializes through map[string]json.RawMessage rather than re-running
// the endpoint's normalizer: every other member survives verbatim, which is the
// same contract metering.RewriteBody keeps, and it needs no change to the
// normalizeFunc signature that four endpoints and their tests share.
//
// The Responses API is the reason this takes an endpoint at all: its body
// carries a ResponsesUsage (input_tokens / output_tokens), not the
// UsageResponse shape chat and legacy completions emit, so writing the wrong
// one in would corrupt the response rather than correct it.
//
// On any error the ORIGINAL bytes are returned. A failed cosmetic rewrite must
// never fail a delivered response; the charge is already bounded by the clamped
// usage object regardless of what these bytes say.
func rewriteNormalizedUsage(normalized []byte, endpoint string, usage *UsageResponse) []byte {
	if len(normalized) == 0 || usage == nil {
		return normalized
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &fields); err != nil || fields == nil {
		log.Printf("inference: could not rewrite clamped usage into the response body endpoint=%s: %v", endpoint, err)
		return normalized
	}
	if _, present := fields["usage"]; !present {
		return normalized
	}

	var raw []byte
	var err error
	if endpoint == EndpointResponses {
		raw, err = json.Marshal(chatToResponsesUsage(usage))
	} else {
		raw, err = json.Marshal(usage)
	}
	if err != nil {
		log.Printf("inference: could not encode clamped usage endpoint=%s: %v", endpoint, err)
		return normalized
	}
	fields["usage"] = raw

	out, err := json.Marshal(fields)
	if err != nil {
		log.Printf("inference: could not re-encode the response body after a usage clamp endpoint=%s: %v", endpoint, err)
		return normalized
	}
	return out
}

// capCaptureAtCeiling bounds a settlement that charges the reservation HOLD
// rather than a measured quantity, so the same invariant covers it.
//
// The zero-content capture (issue #1171) charges the hold when a completion
// came back empty even after its retry. The hold is a flat authorization floor
// (DefaultHoldText, a $0.10 equivalent), so capturing it against a request
// capped at 8 completion tokens breaches this invariant by a far wider margin
// than the overrun #1283 reported. Bounding it keeps the capture fail-closed --
// it still charges, and CreditsForTokens floors any non-zero quantity at one
// credit, so it can never become the free serve D-034 exists to prevent -- while
// keeping it inside what the caller authorized by their own ceiling.
//
// A variable-price alias is left alone: CreditsForTokens has no catalog price to
// read there and says so loudly, so there is no ceiling price to compare.
func capCaptureAtCeiling(route SelectRouteResult, ceiling, freshInputTokens, cacheReadTokens, cacheWriteTokens, credits int64) int64 {
	if ceiling <= 0 || route.Pricing.IsUpstreamActual() {
		return credits
	}
	bound := CreditsForTokens(route, freshInputTokens, cacheReadTokens, cacheWriteTokens, ceiling)
	if bound < credits {
		return bound
	}
	return credits
}
