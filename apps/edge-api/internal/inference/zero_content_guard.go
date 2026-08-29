package inference

import (
	"encoding/json"
	"strings"
)

// Zero-content guard (issue #1171).
//
// The hive-free alias load-balances heterogeneous members behind one
// litellm_model_name: edge-api dispatches to a pool, never to a member,
// because LiteLLM's router picks whichever deployment answers. Three of its
// four members reason; a reasoning member can spend the caller's entire
// max_tokens on hidden reasoning and answer finish_reason=length with no
// visible content at all, which used to settle as an ordinary full-price
// success (live evidence on #1171: five of six reasoning prompts returned
// empty content and were billed in full).
//
// ZERO-CONTENT GUARD, post-response on the sync path. A chat completion whose
// every choice carries finish_reason=length with no visible output and no tool
// call is retried once against the same pool. If the retry is empty too (or
// the retry itself fails), the request settles fail-closed: capture the
// reservation hold with terminal_usage_confirmed=false, bounded by the
// caller's own completion ceiling (capCaptureAtCeiling, issue #1283), the same
// capture shape UpstreamActualSettlement has always applied to variable-price
// aliases and PR #1220 extends to streams. A loud alarm counter
// (hive_zero_content_captured_total) plus an honest caller-facing flag
// (X-Hive-Upstream-Empty-Content response header) accompany it.
//
// WHAT USED TO LIVE HERE, and why it does not any more. PR #1225 paired the
// guard with pre-dispatch HEADROOM: when route.ReasoningReserveTokens was
// positive, every completion-limit field the caller had set was inflated by
// that reserve before dispatch, on the premise that hidden reasoning would
// burn the reserve while visible content survived inside the caller's own
// budget. The premise is not enforceable. Nothing in an OpenAI-compatible
// request tells an upstream that part of a ceiling is earmarked for hidden
// reasoning, so the model spends the inflated ceiling on whichever it emits;
// and because the rewrite skipped a ceiling the caller had NOT set, its entire
// effect was overriding ceilings callers HAD set. Live on 2026-08-28
// (issue #1283): max_tokens: 8 went upstream as 4104 and came back as 1650
// billed completion tokens. The caller's ceiling now reaches the provider
// untouched, which is also what OpenAI specifies for a reasoning model, where
// reasoning tokens count against the ceiling. provider_routes.
// reasoning_reserve_tokens survives as what gates this guard: it marks a pool
// whose members reason, which is exactly the pool where an empty-length answer
// means a reasoning burn rather than an upstream fault.

const (
	// emptyContentHeader names the flag surfaced to a caller whose sync chat
	// completion came back with no visible output even after one retry had
	// already run. The value records the upstream finish_reason that
	// produced it.
	emptyContentHeader = "X-Hive-Upstream-Empty-Content"

	emptyContentHeaderValue = "length"
)

// isEmptyLengthCompletion reports whether normalized is a chat completion in
// the reasoning-burn shape: at least one choice, every choice finishing with
// finish_reason=length carrying no visible content and no tool call or legacy
// function call. It is the detector behind the sync-path guard.
//
// Deliberately narrow:
//   - finish_reason=stop with empty text is NOT matched. Genuine empty answers
//     exist (the usage_clamp comment calls them legitimate), and a stop-finish
//     means the model chose to end; rewriting that settlement would charge
//     nothing for a delivered response the upstream called complete.
//   - A tool-call message with null content is spec-correct (coerceNullContent
//     leaves it alone for the same reason) and must not trip the guard.
//   - Refusal strings count as visible output: they are generated assistant
//     output the caller can act on, same as chatChoiceTexts treats them.
func isEmptyLengthCompletion(normalized []byte) bool {
	var resp ChatCompletionResponse
	if err := json.Unmarshal(normalized, &resp); err != nil {
		return false
	}
	if len(resp.Choices) == 0 {
		return false
	}
	for _, choice := range resp.Choices {
		if choice.FinishReason == nil || !strings.EqualFold(*choice.FinishReason, "length") {
			return false
		}
		if choice.Message.Content != nil && *choice.Message.Content != "" {
			return false
		}
		if rawFieldPresent(choice.Message.ToolCalls) || rawFieldPresent(choice.Message.FunctionCall) {
			return false
		}
		if choice.Message.Refusal != nil && *choice.Message.Refusal != "" {
			return false
		}
	}
	return true
}
