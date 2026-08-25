package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FromOAIResponse lifts an OAIResponse to an Anthropic MessagesResponse.
// clientAlias is the model name the client sent; it is echoed back verbatim
// so upstream route identifiers (e.g. openrouter/...) never reach the client.
func FromOAIResponse(resp OAIResponse, clientAlias string) MessagesResponse {
	id := resp.ID
	if !strings.HasPrefix(id, "msg_") {
		id = "msg_" + id
	}

	model := clientAlias
	if model == "" {
		model = resp.Model
	}

	out := MessagesResponse{
		ID:    id,
		Type:  "message",
		Role:  "assistant",
		Model: model,
		Usage: anthropicUsage(resp.Usage),
	}

	if len(resp.Choices) == 0 {
		out.StopReason = "end_turn"
		return out
	}

	choice := resp.Choices[0]
	out.StopReason = mapFinishReason(choice.FinishReason)

	var blocks []ResponseBlock

	if choice.Message.Content != "" {
		blocks = append(blocks, ResponseBlock{
			Type: "text",
			Text: choice.Message.Content,
		})
	}

	for _, tc := range choice.Message.ToolCalls {
		input, err := parseToolArguments(tc.Function.Arguments)
		if err != nil {
			input = json.RawMessage(fmt.Sprintf(`{"_raw":%q}`, tc.Function.Arguments))
		}
		blocks = append(blocks, ResponseBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	out.Content = blocks
	return out
}

// anthropicUsage converts an inclusive OpenAI/OpenRouter usage object into the
// exclusive shape Anthropic clients (Claude Code included) read cache savings
// from. See freshInputTokens for why this is a subtraction, never an
// addition, and ResponseUsage's doc comment for the shape itself.
func anthropicUsage(u OAIUsage) ResponseUsage {
	cacheRead, cacheWrite := 0, 0
	if u.PromptTokensDetails != nil {
		cacheRead = u.PromptTokensDetails.CachedTokens
		cacheWrite = u.PromptTokensDetails.CacheWriteTokens
	}
	return ResponseUsage{
		InputTokens:              freshInputTokens(u.PromptTokens, cacheRead, cacheWrite),
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
// A negative result here is clamped to zero rather than surfaced to the
// client: it means the upstream inclusive-shape assumption broke, which is a
// billing-accuracy alarm for the settlement path (apps/edge-api/internal/inference),
// not something this wire-shape projection can diagnose or repair. This
// function only decides what the client sees, never what gets billed.
func freshInputTokens(promptTokens, cacheRead, cacheWrite int) int {
	fresh := promptTokens - cacheRead - cacheWrite
	if fresh < 0 {
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
