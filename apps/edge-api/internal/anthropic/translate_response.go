package anthropic

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// FromOAIResponse lifts an OAIResponse to an Anthropic MessagesResponse.
// clientAlias is the model name the client sent; it is echoed back verbatim
// so upstream route identifiers (e.g. openrouter/...) never reach the client.
//
// The response id is always freshly minted, never derived from resp.ID.
// resp.ID carries the upstream's own id verbatim (OpenRouter's "gen-*",
// Groq's "chatcmpl-*"), and prior code merely prefixed that value with
// "msg_" -- which still shipped the raw upstream id to the client inside the
// prefix, the same class of leak mintCompletionID's doc comment describes
// for the OpenAI-shaped surface (CLAUDE.md: "provider names never leak to
// customers"). Nothing downstream correlates on this id: request/billing
// correlation keys on this gateway's own attempt.ID, never on anything from
// the /v1/messages response body.
func FromOAIResponse(resp OAIResponse, clientAlias string) MessagesResponse {
	id := "msg_" + uuid.New().String()

	model := clientAlias
	if model == "" {
		model = resp.Model
	}

	out := MessagesResponse{
		ID:    id,
		Type:  "message",
		Role:  "assistant",
		Model: model,
		// Content is seeded with an empty, non-nil slice, and appended to
		// below, so that a turn which produced neither text nor a tool call
		// still serializes as "content":[]. A nil Go slice marshals to JSON
		// null, and Anthropic's contract is that content is ALWAYS an array:
		// every typed client iterates it unconditionally, so null is a
		// TypeError rather than an empty turn (issue #1260). Both exits below
		// are covered by seeding it here rather than at one of them.
		Content: []ResponseBlock{},
		Usage:   anthropicUsage(resp.Usage, model, resp.Model),
	}

	if len(resp.Choices) == 0 {
		out.StopReason = "end_turn"
		return out
	}

	choice := resp.Choices[0]
	out.StopReason = mapFinishReason(choice.FinishReason)

	if choice.Message.Content != "" {
		out.Content = append(out.Content, ResponseBlock{
			Type: "text",
			Text: choice.Message.Content,
		})
	}

	for _, tc := range choice.Message.ToolCalls {
		input, err := parseToolArguments(tc.Function.Arguments)
		if err != nil {
			input = json.RawMessage(fmt.Sprintf(`{"_raw":%q}`, tc.Function.Arguments))
		}
		out.Content = append(out.Content, ResponseBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	return out
}

// anthropicUsage converts an inclusive OpenAI/OpenRouter usage object into the
// exclusive shape Anthropic clients (Claude Code included) read cache savings
// from. clientAlias and upstreamModel exist only to name the alarm in
// freshInputTokens; see its doc comment for why this is a subtraction, never
// an addition, and ResponseUsage's doc comment for the shape itself.
func anthropicUsage(u OAIUsage, clientAlias, upstreamModel string) ResponseUsage {
	cacheRead, cacheWrite := 0, 0
	if u.PromptTokensDetails != nil {
		cacheRead = u.PromptTokensDetails.CachedTokens
		cacheWrite = u.PromptTokensDetails.CacheWriteTokens
	}
	return ResponseUsage{
		InputTokens:              freshInputTokens(u.PromptTokens, cacheRead, cacheWrite, clientAlias, upstreamModel),
		OutputTokens:             u.CompletionTokens,
		CacheCreationInputTokens: cacheWrite,
		CacheReadInputTokens:     cacheRead,
	}
}

// freshInputTokens recovers the Anthropic-exclusive "fresh, uncached" input
// count from OpenRouter's inclusive prompt_tokens, which already contains
// both the cache read and cache write tokens: fresh = prompt - read - write,
// SUBTRACT never ADD (Anthropic-native reporting, which this gateway never
// actually receives since every route goes through OpenRouter, is the
// opposite ADD convention -- getting these swapped either nearly doubles
// billed input on every warm turn or, subtracting on top of an
// already-exclusive number, drives it negative).
//
// A negative result is CLAMPED to zero for the client-facing wire (never a
// negative token count) AND ALARMED via slog.Warn naming clientAlias and
// upstreamModel: per the cache-pricing research doc's section 5, a negative
// here means the upstream inclusive/exclusive shape assumption broke, and
// this is the one place in the request lifecycle that already has the
// numbers in hand to catch it. The equivalent alarm for the actual BILLED
// amount belongs to inference/pricing.go's CreditsForTokens
// (feat/cache-aware-billing, out of this package's scope) -- this function
// only ever decides what the client sees, never what gets billed.
func freshInputTokens(promptTokens, cacheRead, cacheWrite int, clientAlias, upstreamModel string) int {
	fresh := promptTokens - cacheRead - cacheWrite
	if fresh < 0 {
		slog.Warn("anthropic cache usage: fresh input tokens went negative, clamped to zero",
			"alias", clientAlias, "upstream_model", upstreamModel,
			"prompt_tokens", promptTokens, "cache_read_tokens", cacheRead, "cache_write_tokens", cacheWrite)
		return 0
	}
	return fresh
}

// mapFinishReason converts an OpenAI finish_reason to an Anthropic stop_reason.
func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "refusal"
	default:
		return "end_turn"
	}
}

// parseToolArguments parses a JSON-stringified arguments string into RawMessage.
func parseToolArguments(args string) (json.RawMessage, error) {
	if args == "" {
		return json.RawMessage(`{}`), nil
	}
	var check interface{}
	if err := json.Unmarshal([]byte(args), &check); err != nil {
		return nil, err
	}
	return json.RawMessage(args), nil
}
