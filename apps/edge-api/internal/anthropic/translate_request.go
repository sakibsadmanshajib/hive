package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToOAIRequest lowers an Anthropic MessagesRequest to an internal OpenAI-shaped
// OAIRequest that can be forwarded through the existing LiteLLM dispatch path.
// It never leaks provider names; the model alias is passed through as-is so the
// catalog layer resolves it to the appropriate route.
func ToOAIRequest(req MessagesRequest) (OAIRequest, error) {
	var msgs []OAIMessage

	if req.System.Text != "" {
		msgs = append(msgs, OAIMessage{
			Role:    "system",
			Content: OAIMessageContent{Text: req.System.Text},
		})
	}

	for _, m := range req.Messages {
		oai, err := convertMessage(m)
		if err != nil {
			return OAIRequest{}, fmt.Errorf("message role=%s: %w", m.Role, err)
		}
		msgs = append(msgs, oai)
	}

	out := OAIRequest{
		Model:       req.Model,
		Messages:    msgs,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		out.MaxTokens = &mt
	}
	if len(req.StopSequences) > 0 {
		out.Stop = req.StopSequences
	}

	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return OAIRequest{}, fmt.Errorf("tools: %w", err)
		}
		out.Tools = tools
	}

	if req.ToolChoice != nil {
		tc := convertToolChoice(req.ToolChoice)
		out.ToolChoice = &tc
	}

	return out, nil
}

// convertMessage converts a single Anthropic message to OAIMessage.
func convertMessage(m Message) (OAIMessage, error) {
	// Simple string content.
	if m.Content.Text != "" {
		return OAIMessage{Role: m.Role, Content: OAIMessageContent{Text: m.Content.Text}}, nil
	}

	// No content blocks; treat as empty string message.
	if len(m.Content.Blocks) == 0 {
		return OAIMessage{Role: m.Role, Content: OAIMessageContent{Text: ""}}, nil
	}

	// Validate: a tool_result block must be the only block in the message.
	// The Anthropic protocol requires each tool result to be its own message.
	// Mixed non-tool_result blocks before a tool_result are an encoding error.
	for i, bl := range m.Content.Blocks {
		if bl.Type == "tool_result" {
			if i > 0 {
				return OAIMessage{}, fmt.Errorf(
					"tool_result block at index %d has %d preceding block(s); "+
						"each tool_result must be the sole block in its message",
					i, i,
				)
			}
			content := toolResultText(bl)
			return OAIMessage{
				Role:       "tool",
				Content:    OAIMessageContent{Text: content},
				ToolCallID: bl.ToolUseID,
			}, nil
		}
	}

	// Mixed content blocks: text, image, tool_use.
	var parts []OAIContentPart
	var toolCalls []OAIToolCall

	for _, bl := range m.Content.Blocks {
		switch bl.Type {
		case "text":
			parts = append(parts, OAIContentPart{Type: "text", Text: bl.Text})

		case "image":
			if bl.Source == nil {
				continue
			}
			var dataURI string
			if bl.Source.Type == "base64" {
				dataURI = "data:" + bl.Source.MediaType + ";base64," + bl.Source.Data
			} else {
				dataURI = bl.Source.URL
			}
			parts = append(parts, OAIContentPart{
				Type:     "image_url",
				ImageURL: &OAIImageURL{URL: dataURI},
			})

		case "tool_use":
			args := "{}"
			if len(bl.Input) > 0 {
				args = string(bl.Input)
			}
			toolCalls = append(toolCalls, OAIToolCall{
				ID:   bl.ID,
				Type: "function",
				Function: OAIFunctionCall{
					Name:      bl.Name,
					Arguments: args,
				},
			})
		}
	}

	// Pure tool-call assistant turn.
	if len(toolCalls) > 0 && len(parts) == 0 {
		return OAIMessage{Role: m.Role, ToolCalls: toolCalls}, nil
	}
	// Tool calls plus text content.
	if len(toolCalls) > 0 {
		var contentStr string
		for _, p := range parts {
			if p.Type == "text" {
				contentStr += p.Text
			}
		}
		return OAIMessage{
			Role:      m.Role,
			Content:   OAIMessageContent{Text: contentStr},
			ToolCalls: toolCalls,
		}, nil
	}

	// Pure text/image parts: use array form for vision, string for text-only.
	if len(parts) == 1 && parts[0].Type == "text" {
		return OAIMessage{Role: m.Role, Content: OAIMessageContent{Text: parts[0].Text}}, nil
	}
	return OAIMessage{Role: m.Role, Content: OAIMessageContent{Parts: parts}}, nil
}

// toolResultText extracts the text content from a tool_result block.
func toolResultText(bl ContentBlock) string {
	if bl.Content == nil {
		return ""
	}
	if bl.Content.Text != "" {
		return bl.Content.Text
	}
	var sb strings.Builder
	for _, b := range bl.Content.Blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// convertTools maps Anthropic tool definitions to OAI function tools.
func convertTools(tools []Tool) ([]OAITool, error) {
	out := make([]OAITool, 0, len(tools))
	for _, t := range tools {
		params := t.InputSchema
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		out = append(out, OAITool{
			Type: "function",
			Function: OAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	return out, nil
}

// convertToolChoice maps Anthropic tool_choice to the typed OAIToolChoice.
//
//	auto        -> sentinel "auto"
//	any         -> sentinel "required"
//	{type:tool} -> named function selector
func convertToolChoice(tc *ToolChoice) OAIToolChoice {
	switch tc.Type {
	case "auto":
		return OAIToolChoice{Sentinel: "auto"}
	case "any":
		return OAIToolChoice{Sentinel: "required"}
	case "tool":
		return OAIToolChoice{
			Named: &OAINamedToolChoice{
				Type: "function",
				Function: OAINamedToolChoiceFunction{Name: tc.Name},
			},
		}
	default:
		return OAIToolChoice{Sentinel: "auto"}
	}
}
