package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxSessionIDLen is OpenRouter's documented ceiling for the sticky-routing
// session_id passthrough (see MessagesRequest.SessionID). Truncated rather
// than rejected: it is a routing hint, not a correctness-bearing field.
const maxSessionIDLen = 256

// ToOAIRequest lowers an Anthropic MessagesRequest to an internal OpenAI-shaped
// OAIRequest that can be forwarded through the existing LiteLLM dispatch path.
// It never leaks provider names; the model alias is passed through as-is so the
// catalog layer resolves it to the appropriate route.
func ToOAIRequest(req MessagesRequest) (OAIRequest, error) {
	var msgs []OAIMessage

	if req.System.Text != "" || len(req.System.Blocks) > 0 {
		msgs = append(msgs, OAIMessage{
			Role:    "system",
			Content: systemContent(req.System),
		})
	}

	for _, m := range req.Messages {
		expanded, err := convertMessage(m)
		if err != nil {
			return OAIRequest{}, fmt.Errorf("message role=%s: %w", m.Role, err)
		}
		msgs = append(msgs, expanded...)
	}

	out := OAIRequest{
		Model:        req.Model,
		Messages:     msgs,
		Temperature:  req.Temperature,
		TopP:         req.TopP,
		Stream:       req.Stream,
		CacheControl: req.CacheControl,
	}

	if req.SessionID != "" {
		id := req.SessionID
		if len(id) > maxSessionIDLen {
			id = id[:maxSessionIDLen]
		}
		out.SessionID = id
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

// systemContent lowers a SystemField to an OAIMessageContent. When no block
// carries a cache breakpoint it reproduces the exact plain-string output this
// function always emitted (the regression guard: an uncached system prompt
// must serialize identically to before this change), because collapsing to a
// flat string cannot represent a per-block cache_control -- a request that
// does use one gets the block-array form instead, each block's own
// CacheControl carried onto its OAIContentPart.
func systemContent(sf SystemField) OAIMessageContent {
	needsBlocks := false
	for _, bl := range sf.Blocks {
		if bl.CacheControl != nil {
			needsBlocks = true
			break
		}
	}
	if !needsBlocks {
		return OAIMessageContent{Text: sf.Text}
	}

	parts := make([]OAIContentPart, 0, len(sf.Blocks))
	for _, bl := range sf.Blocks {
		if bl.Type != "text" {
			// Anthropic's system field only ever carries text blocks; a
			// non-text block here would already have been silently ignored
			// by SystemField.UnmarshalJSON's Text concatenation, so dropping
			// it here too is not a new gap this change introduces.
			continue
		}
		parts = append(parts, OAIContentPart{
			Type:         "text",
			Text:         bl.Text,
			CacheControl: bl.CacheControl,
		})
	}
	return OAIMessageContent{Parts: parts}
}

// convertMessage converts a single Anthropic message to one or more OAIMessages.
// Parallel tool calls send multiple tool_result blocks in one Anthropic message;
// each must become a separate OAI "tool" message so the LLM receives all results.
func convertMessage(m Message) ([]OAIMessage, error) {
	// Simple string content.
	if m.Content.Text != "" {
		return []OAIMessage{{Role: m.Role, Content: OAIMessageContent{Text: m.Content.Text}}}, nil
	}

	// No content blocks; treat as empty string message.
	if len(m.Content.Blocks) == 0 {
		return []OAIMessage{{Role: m.Role, Content: OAIMessageContent{Text: ""}}}, nil
	}

	// Collect all tool_result blocks first. If any exist, every block must be a
	// tool_result (mixing non-tool_result blocks with tool_results is an error).
	var toolResults []OAIMessage
	for i, bl := range m.Content.Blocks {
		if bl.Type == "tool_result" {
			if len(toolResults) == 0 && i > 0 {
				// There were non-tool_result blocks before this one.
				return nil, fmt.Errorf(
					"tool_result block at index %d has %d preceding block(s); "+
						"each tool_result must be the sole block type in its message",
					i, i,
				)
			}
			content := toolResultText(bl)
			toolResults = append(toolResults, OAIMessage{
				Role:         "tool",
				Content:      OAIMessageContent{Text: content},
				ToolCallID:   bl.ToolUseID,
				CacheControl: bl.CacheControl,
			})
		} else if len(toolResults) > 0 {
			// tool_result blocks were already seen; a non-tool_result block after them is invalid.
			return nil, fmt.Errorf(
				"non-tool_result block %q at index %d follows tool_result blocks; "+
					"each tool_result must be the sole block type in its message",
				bl.Type, i,
			)
		}
	}
	if len(toolResults) > 0 {
		return toolResults, nil
	}

	// Mixed content blocks: text, image, tool_use.
	var parts []OAIContentPart
	var toolCalls []OAIToolCall

	for _, bl := range m.Content.Blocks {
		switch bl.Type {
		case "text":
			parts = append(parts, OAIContentPart{Type: "text", Text: bl.Text, CacheControl: bl.CacheControl})

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
				Type:         "image_url",
				ImageURL:     &OAIImageURL{URL: dataURI},
				CacheControl: bl.CacheControl,
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
				CacheControl: bl.CacheControl,
			})
		}
	}

	// Pure tool-call assistant turn.
	if len(toolCalls) > 0 && len(parts) == 0 {
		return []OAIMessage{{Role: m.Role, ToolCalls: toolCalls}}, nil
	}
	// Tool calls plus text/image content. A part with a cache breakpoint
	// keeps the block-array form so it is not lost; otherwise this
	// reproduces the exact flat-string concatenation this function always
	// emitted (including its pre-existing wart of silently dropping any
	// image part mixed with tool calls -- see the PR body's dropped-field
	// audit; fixing that is a separate, non-cache_control change and out of
	// this fix's surgical scope), which is the regression guard for the
	// overwhelmingly common no-cache-control case.
	if len(toolCalls) > 0 {
		if partsNeedArrayForm(parts) {
			return []OAIMessage{{
				Role:      m.Role,
				Content:   OAIMessageContent{Parts: parts},
				ToolCalls: toolCalls,
			}}, nil
		}
		var contentStr string
		for _, p := range parts {
			if p.Type == "text" {
				contentStr += p.Text
			}
		}
		return []OAIMessage{{
			Role:      m.Role,
			Content:   OAIMessageContent{Text: contentStr},
			ToolCalls: toolCalls,
		}}, nil
	}

	// Pure text/image parts: use array form for vision or a cache breakpoint,
	// string for a lone uncached text block.
	if len(parts) == 1 && parts[0].Type == "text" && parts[0].CacheControl == nil {
		return []OAIMessage{{Role: m.Role, Content: OAIMessageContent{Text: parts[0].Text}}}, nil
	}
	return []OAIMessage{{Role: m.Role, Content: OAIMessageContent{Parts: parts}}}, nil
}

// partsNeedArrayForm reports whether a set of content parts must keep the
// block-array form rather than collapse to a flat string: true if any part
// carries a cache breakpoint, which a flat string cannot address.
func partsNeedArrayForm(parts []OAIContentPart) bool {
	for _, p := range parts {
		if p.CacheControl != nil {
			return true
		}
	}
	return false
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
			CacheControl: t.CacheControl,
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
				Type:     "function",
				Function: OAINamedToolChoiceFunction{Name: tc.Name},
			},
		}
	default:
		return OAIToolChoice{Sentinel: "auto"}
	}
}
