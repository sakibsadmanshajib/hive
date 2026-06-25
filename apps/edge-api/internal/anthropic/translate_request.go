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

	// Prepend system message when present.
	if req.System.Text != "" {
		msgs = append(msgs, OAIMessage{Role: "system", Content: req.System.Text})
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
		out.ToolChoice = convertToolChoice(req.ToolChoice)
	}

	return out, nil
}

// convertMessage converts a single Anthropic message to OAIMessage.
func convertMessage(m Message) (OAIMessage, error) {
	// Simple string content.
	if m.Content.Text != "" {
		return OAIMessage{Role: m.Role, Content: m.Content.Text}, nil
	}

	// No content blocks; treat as empty string message.
	if len(m.Content.Blocks) == 0 {
		return OAIMessage{Role: m.Role, Content: ""}, nil
	}

	// Mixed content blocks: may need multipart content or tool_calls.
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
			// assistant tool_use block becomes an OAI tool_calls entry.
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

		case "tool_result":
			// tool_result becomes an OpenAI tool message.
			// We handle this as a standalone message below; for now collect text.
			content := toolResultText(bl)
			return OAIMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: bl.ToolUseID,
			}, nil
		}
	}

	// If we collected tool_calls with no content parts, it is a pure tool-call
	// assistant turn.
	if len(toolCalls) > 0 && len(parts) == 0 {
		return OAIMessage{Role: m.Role, ToolCalls: toolCalls}, nil
	}
	// Tool calls plus text content.
	if len(toolCalls) > 0 {
		// OAI allows content + tool_calls simultaneously on assistant messages.
		var contentStr string
		for _, p := range parts {
			if p.Type == "text" {
				contentStr += p.Text
			}
		}
		return OAIMessage{Role: m.Role, Content: contentStr, ToolCalls: toolCalls}, nil
	}

	// Pure text/image parts: use array form for vision, string for text-only.
	if len(parts) == 1 && parts[0].Type == "text" {
		return OAIMessage{Role: m.Role, Content: parts[0].Text}, nil
	}
	return OAIMessage{Role: m.Role, Content: parts}, nil
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

// convertToolChoice maps Anthropic tool_choice to the OpenAI equivalent.
//
//   auto        -> "auto"
//   any         -> "required"
//   {type:tool} -> {type:"function", function:{name:...}}
func convertToolChoice(tc *ToolChoice) interface{} {
	switch tc.Type {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		return map[string]interface{}{
			"type": "function",
			"function": map[string]string{
				"name": tc.Name,
			},
		}
	default:
		return "auto"
	}
}
