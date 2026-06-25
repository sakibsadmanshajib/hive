package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FromOAIResponse lifts an OAIResponse (OpenAI chat completions shape) to an
// Anthropic MessagesResponse. The model field is echoed from the OAI response
// so downstream callers see the alias they requested, never an upstream route.
func FromOAIResponse(resp OAIResponse) MessagesResponse {
	id := resp.ID
	if !strings.HasPrefix(id, "msg_") {
		id = "msg_" + id
	}

	out := MessagesResponse{
		ID:    id,
		Type:  "message",
		Role:  "assistant",
		Model: resp.Model,
		Usage: ResponseUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	if len(resp.Choices) == 0 {
		out.StopReason = "end_turn"
		return out
	}

	choice := resp.Choices[0]
	out.StopReason = mapFinishReason(choice.FinishReason)

	// Build content blocks from the choice message.
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
			// Preserve raw JSON as a fallback; never drop a tool call silently.
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

// mapFinishReason converts an OpenAI finish_reason to an Anthropic stop_reason.
// Validated against the Anthropic stop_reason enum:
// end_turn | max_tokens | stop_sequence | tool_use | refusal
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

// parseToolArguments parses a JSON-stringified function arguments string into
// a json.RawMessage suitable for the Anthropic tool_use input field.
func parseToolArguments(args string) (json.RawMessage, error) {
	if args == "" {
		return json.RawMessage(`{}`), nil
	}
	// Validate it is valid JSON.
	var check interface{}
	if err := json.Unmarshal([]byte(args), &check); err != nil {
		return nil, err
	}
	return json.RawMessage(args), nil
}
