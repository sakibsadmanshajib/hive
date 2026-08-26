package inference

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
)

// Reasoning headroom + zero-content guard (issue #1171).
//
// The hive-free alias load-balances heterogeneous members behind one
// litellm_model_name: edge-api dispatches to a pool, never to a member,
// because LiteLLM's router picks whichever deployment answers. Two of its
// four members reason; a reasoning member can spend the caller's entire
// max_tokens on hidden reasoning and answer finish_reason=length with no
// visible content at all, which used to settle as an ordinary full-price
// success (live evidence on #1171: five of six reasoning prompts returned
// empty content and were billed in full). Two mechanisms close that.
//
//  1. HEADROOM, pre-dispatch. When the selection carries
//     route.ReasoningReserveTokens > 0, inflate every completion-limit field
//     the caller actually set by that reserve before dispatch. Hidden
//     reasoning burns the reserve; visible content survives inside the
//     caller's own budget, so their requested max_tokens keeps its OpenAI
//     meaning: it caps what they see.
//
//  2. ZERO-CONTENT GUARD, post-response on the sync path. A chat completion
//     whose every choice carries finish_reason=length with no visible output
//     and no tool call is retried once against the same pool. If the retry is
//     empty too (or the retry itself fails), the request settles fail-closed:
//     capture the reservation hold at its own size with
//     terminal_usage_confirmed=false, the same capture shape
//     UpstreamActualSettlement has always applied to variable-price aliases
//     and PR #1220 extends to streams. A loud alarm counter
//     (hive_zero_content_captured_total) plus an honest caller-facing flag
//     (X-Hive-Upstream-Empty-Content response header) accompany it.

const (
	// emptyContentHeader names the flag surfaced to a caller whose sync chat
	// completion came back with no visible output even after one retry had
	// already run. The value records the upstream finish_reason that
	// produced it.
	emptyContentHeader = "X-Hive-Upstream-Empty-Content"

	emptyContentHeaderValue = "length"
)

// applyReasoningHeadroom inflates the completion-limit fields present in the
// request body by reserveTokens and returns the rewritten body plus whether
// anything changed. It reads completionLimitFields so each endpoint inflates
// exactly the ceiling fields that endpoint speaks: chat carries both
// spellings (max_tokens and max_completion_tokens), legacy completions only
// max_tokens, responses only max_output_tokens.
//
//   - reserveTokens <= 0 or an empty body: unchanged, which is the common
//     case for every non-reasoning alias.
//   - A limit field absent or non-positive is left alone: a caller who set
//     no ceiling declared no budget for this gateway to protect, and
//     upstream defaults apply as they always did.
//   - Both chat spellings present: both inflated, preserving the relative
//     gap between them.
//   - Unparseable JSON returns unchanged. EnforceVariablePriceBounds already
//     refuses unparseable bodies before dispatch on variable-price aliases;
//     elsewhere a second silent opinion about a malformed request would be
//     worse than passing it through.
func applyReasoningHeadroom(body []byte, endpoint string, reserveTokens int) ([]byte, bool) {
	if reserveTokens <= 0 || len(body) == 0 {
		return body, false
	}
	fields := completionLimitFields[endpoint]
	if len(fields) == 0 {
		return body, false
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil || decoded == nil {
		return body, false
	}

	changed := false
	for _, field := range fields {
		raw, present := decoded[field]
		if !present {
			continue
		}
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil || value <= 0 {
			continue
		}
		decoded[field] = json.RawMessage(strconv.FormatInt(value+int64(reserveTokens), 10))
		changed = true
	}
	if !changed {
		return body, false
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		// Marshal of a map[string]json.RawMessage built from already-valid
		// JSON cannot fail in practice; if it somehow does, dispatch the
		// original body rather than inventing a failure the caller never
		// asked about.
		log.Printf("inference: reasoning headroom rewrite failed endpoint=%s: %v", endpoint, err)
		return body, false
	}
	return out, true
}

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
