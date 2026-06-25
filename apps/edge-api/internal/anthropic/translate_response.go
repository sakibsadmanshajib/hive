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
