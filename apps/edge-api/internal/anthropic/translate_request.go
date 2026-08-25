package anthropic

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"
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
		TopK:         req.TopK,
		Stream:       req.Stream,
		CacheControl: req.CacheControl,
		Thinking:     req.Thinking,
	}

	if req.Metadata != nil && req.Metadata.UserID != "" {
		out.User = req.Metadata.UserID
	}

	if req.SessionID != "" {
		id, truncated := truncateSessionID(req.SessionID)
		if truncated {
			slog.Warn("anthropic session_id truncated to the OpenRouter ceiling",
				"original_len", len(req.SessionID), "truncated_len", len(id))
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
		if req.ToolChoice.DisableParallelToolUse != nil && *req.ToolChoice.DisableParallelToolUse {
			no := false
			out.ParallelToolCalls = &no
		}
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

	// Mixed content blocks: text, image, tool_use, thinking.
	var parts []OAIContentPart
	var toolCalls []OAIToolCall
	var thinkingBlocks []OAIThinkingBlock

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

		case "thinking", "redacted_thinking":
			// Prior-turn extended-thinking content, echoed back by the client
			// as conversation history. Before this fix these blocks matched no
			// case here at all: a thinking-only message fell through with
			// empty parts and empty toolCalls, and every branch below produced
			// an OAIMessage with no content and no tool_calls -- silently
			// empty, not an error. thinkingBlocks is attached to every return
			// path below so this can never happen again, whether the thinking
			// block is alone or mixed with text/tool_use in the same turn.
			thinkingBlocks = append(thinkingBlocks, OAIThinkingBlock{
				Type:      bl.Type,
				Thinking:  bl.Thinking,
				Signature: bl.Signature,
				Data:      bl.Data,
			})
		}
	}

	// Pure tool-call assistant turn.
	if len(toolCalls) > 0 && len(parts) == 0 {
		return []OAIMessage{{Role: m.Role, ToolCalls: toolCalls, ThinkingBlocks: thinkingBlocks}}, nil
	}
	// Tool calls plus text/image content. A part with a cache breakpoint, or a
	// non-text part (image), keeps the block-array form so it is not lost;
	// otherwise this reproduces the exact flat-string concatenation this
	// function always emitted, which is the regression guard for the
	// overwhelmingly common plain-text case.
	if len(toolCalls) > 0 {
		if partsNeedArrayForm(parts) {
			return []OAIMessage{{
				Role:           m.Role,
				Content:        OAIMessageContent{Parts: parts},
				ToolCalls:      toolCalls,
				ThinkingBlocks: thinkingBlocks,
			}}, nil
		}
		var contentStr string
		for _, p := range parts {
			if p.Type == "text" {
				contentStr += p.Text
			}
		}
		return []OAIMessage{{
			Role:           m.Role,
			Content:        OAIMessageContent{Text: contentStr},
			ToolCalls:      toolCalls,
			ThinkingBlocks: thinkingBlocks,
		}}, nil
	}

	// Pure text/image/thinking parts: use array form for vision or a cache
	// breakpoint, string for a lone uncached text block.
	if len(parts) == 1 && parts[0].Type == "text" && parts[0].CacheControl == nil && len(thinkingBlocks) == 0 {
		return []OAIMessage{{Role: m.Role, Content: OAIMessageContent{Text: parts[0].Text}}}, nil
	}
	return []OAIMessage{{Role: m.Role, Content: OAIMessageContent{Parts: parts}, ThinkingBlocks: thinkingBlocks}}, nil
}

// partsNeedArrayForm reports whether a set of content parts must keep the
// block-array form rather than collapse to a flat string: true if any part
// carries a cache breakpoint (which a flat string cannot address), or any
// part is not plain text (a flat string cannot carry an image_url either --
// this used to silently drop an image mixed with tool calls; see issue
// #1153 item 6).
func partsNeedArrayForm(parts []OAIContentPart) bool {
	for _, p := range parts {
		if p.CacheControl != nil || p.Type != "text" {
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
//	none        -> sentinel "none"
//	{type:tool} -> named function selector
//
// "none" is the case that matters most here: it means "the model must not
// call any tool", and OpenAI's chat/completions surface has the identical
// sentinel for the identical meaning. Before this fix, "none" fell through to
// the default branch below and came out as "auto" -- the opposite of what the
// caller asked for, not merely a dropped field.
func convertToolChoice(tc *ToolChoice) OAIToolChoice {
	switch tc.Type {
	case "auto":
		return OAIToolChoice{Sentinel: "auto"}
	case "any":
		return OAIToolChoice{Sentinel: "required"}
	case "none":
		return OAIToolChoice{Sentinel: "none"}
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

// truncateSessionID cuts id to at most maxSessionIDLen bytes at a rune
// boundary. A plain byte-index slice (id[:maxSessionIDLen]) can land inside
// a multibyte UTF-8 rune; json.Marshal would then silently replace the
// corrupted tail byte with U+FFFD, so the value forwarded to OpenRouter
// would differ from any prefix the caller actually sent. Reports whether it
// actually cut anything, so the caller can log it.
func truncateSessionID(id string) (string, bool) {
	if len(id) <= maxSessionIDLen {
		return id, false
	}
	cut := maxSessionIDLen
	for cut > 0 && !utf8.RuneStart(id[cut]) {
		cut--
	}
	return id[:cut], true
}

// maxCacheControlBreakpoints is Anthropic's documented per-request cap.
// Enforced locally so an unbounded number of breakpoints cannot make Hive
// forward unbounded upstream work on a client's behalf.
const maxCacheControlBreakpoints = 4

// validateCacheControl checks every cache_control breakpoint in the request
// (root, system blocks, message content blocks, tool_result content
// nested blocks are deliberately excluded -- Anthropic's cache_control lives
// on a tool_result block itself, not inside its nested content, so nothing
// there can validly carry one -- and tool definitions) against Anthropic's
// documented shape: type must be "ephemeral", ttl if set must be "5m" or
// "1h", and no more than maxCacheControlBreakpoints total. Anthropic's real
// API already rejects a violation, so this is a pure fail-fast: catching a
// client mistake (or a script fuzzing this endpoint) before a full round
// trip to a paid upstream, not a new restriction on anything valid.
func validateCacheControl(req MessagesRequest) error {
	count := 0
	check := func(cc *CacheControl) error {
		if cc == nil {
			return nil
		}
		count++
		if cc.Type != "ephemeral" {
			return fmt.Errorf("cache_control.type must be %q, got %q", "ephemeral", cc.Type)
		}
		if cc.TTL != "" && cc.TTL != "5m" && cc.TTL != "1h" {
			return fmt.Errorf("cache_control.ttl must be %q or %q, got %q", "5m", "1h", cc.TTL)
		}
		return nil
	}

	if err := check(req.CacheControl); err != nil {
		return err
	}
	for _, bl := range req.System.Blocks {
		if err := check(bl.CacheControl); err != nil {
			return err
		}
	}
	for _, m := range req.Messages {
		for _, bl := range m.Content.Blocks {
			if err := check(bl.CacheControl); err != nil {
				return err
			}
		}
	}
	for _, t := range req.Tools {
		if err := check(t.CacheControl); err != nil {
			return err
		}
	}

	if count > maxCacheControlBreakpoints {
		return fmt.Errorf("at most %d cache_control breakpoints per request, got %d", maxCacheControlBreakpoints, count)
	}
	return nil
}
