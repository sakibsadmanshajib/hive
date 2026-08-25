package anthropic_test

import (
	"encoding/json"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

// TestToOAIRequest_MaximalRequest_NoFieldLost is the structural guard issue
// #1153 asks for: a single Anthropic request that populates every documented
// MessagesRequest field (plus every documented sub-field on the types that
// hang off it -- ContentBlock, Tool, ToolChoice), translated once, with an
// explicit assertion per field. This is what stops the next field silently
// dropped or inverted by translate_request.go from going unnoticed the way
// cache_control did (PR #1152) and the way the six fields fixed by this
// change did (issue #1153): a translator that rebuilds its output field by
// field has no other structural defense against "nobody remembered to carry
// this one across."
//
// When a new field is added to MessagesRequest, the correct response is to
// add it here too, not just in a field-specific test elsewhere in this
// package.
func TestToOAIRequest_MaximalRequest_NoFieldLost(t *testing.T) {
	raw := `{
		"model": "claude-3-5-sonnet",
		"max_tokens": 4096,
		"system": "You are a helpful assistant.",
		"messages": [
			{"role": "user", "content": "What's the weather in Dhaka, and analyze this chart?"},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "I should call the weather tool.", "signature": "sig_001"},
				{"type": "text", "text": "Let me check.", "cache_control": {"type": "ephemeral"}},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "abc123"}},
				{"type": "tool_use", "id": "tu_01", "name": "get_weather", "input": {"city": "Dhaka"}, "cache_control": {"type": "ephemeral"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "tu_01", "content": "Sunny, 32C", "cache_control": {"type": "ephemeral"}}
			]}
		],
		"tools": [
			{"name": "get_weather", "description": "Get current weather", "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}, "cache_control": {"type": "ephemeral"}}
		],
		"tool_choice": {"type": "auto", "disable_parallel_tool_use": true},
		"temperature": 0.65,
		"top_p": 0.92,
		"top_k": 40,
		"stop_sequences": ["STOP", "\n\n"],
		"stream": true,
		"metadata": {"user_id": "end-user-789"},
		"cache_control": {"type": "ephemeral", "ttl": "1h"},
		"session_id": "sess-abc-123",
		"thinking": {"type": "enabled", "budget_tokens": 8192}
	}`

	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}

	// --- Request-root fields ---

	if got.Model != "claude-3-5-sonnet" {
		t.Errorf("model: got %q", got.Model)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 4096 {
		t.Errorf("max_tokens: got %v", got.MaxTokens)
	}
	if got.Temperature == nil || *got.Temperature != 0.65 {
		t.Errorf("temperature: got %v", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.92 {
		t.Errorf("top_p: got %v", got.TopP)
	}
	if got.TopK == nil || *got.TopK != 40 {
		t.Errorf("top_k: got %v (issue #1153 item 4)", got.TopK)
	}
	if len(got.Stop) != 2 || got.Stop[0] != "STOP" || got.Stop[1] != "\n\n" {
		t.Errorf("stop_sequences: got %v", got.Stop)
	}
	if got.Stream != true {
		t.Errorf("stream: got %v", got.Stream)
	}
	if got.User != "end-user-789" {
		t.Errorf("user (from metadata.user_id): got %q (issue #1153 item 2)", got.User)
	}
	if got.CacheControl == nil || got.CacheControl.Type != "ephemeral" || got.CacheControl.TTL != "1h" {
		t.Errorf("cache_control (root): got %+v", got.CacheControl)
	}
	if got.SessionID != "sess-abc-123" {
		t.Errorf("session_id: got %q", got.SessionID)
	}
	if got.Thinking == nil || got.Thinking.Type != "enabled" || got.Thinking.BudgetTokens != 8192 {
		t.Errorf("thinking (request field): got %+v (issue #1153 item 5a)", got.Thinking)
	}

	// --- tool_choice + disable_parallel_tool_use ---

	if got.ToolChoice == nil {
		t.Fatal("tool_choice: nil")
	}
	tcJSON, _ := json.Marshal(got.ToolChoice)
	if string(tcJSON) != `"auto"` {
		t.Errorf("tool_choice: got %s", tcJSON)
	}
	if got.ParallelToolCalls == nil || *got.ParallelToolCalls != false {
		t.Errorf("parallel_tool_calls (from disable_parallel_tool_use): got %v (issue #1153 item 3)", got.ParallelToolCalls)
	}

	// --- tools + tool cache_control ---

	if len(got.Tools) != 1 {
		t.Fatalf("tools len: got %d", len(got.Tools))
	}
	tool := got.Tools[0]
	if tool.Function.Name != "get_weather" || tool.Function.Description != "Get current weather" {
		t.Errorf("tool: got %+v", tool.Function)
	}
	if tool.CacheControl == nil || tool.CacheControl.Type != "ephemeral" {
		t.Errorf("tool cache_control: got %+v", tool.CacheControl)
	}

	// --- messages ---

	if len(got.Messages) != 4 {
		t.Fatalf("messages len: want 4 got %d", len(got.Messages))
	}

	sys := got.Messages[0]
	if sys.Role != "system" {
		t.Errorf("messages[0].role: got %q", sys.Role)
	}
	sysJSON, _ := json.Marshal(sys.Content)
	if string(sysJSON) != `"You are a helpful assistant."` {
		t.Errorf("messages[0].content: got %s", sysJSON)
	}

	userText := got.Messages[1]
	if userText.Role != "user" {
		t.Errorf("messages[1].role: got %q", userText.Role)
	}
	userTextJSON, _ := json.Marshal(userText.Content)
	if string(userTextJSON) != `"What's the weather in Dhaka, and analyze this chart?"` {
		t.Errorf("messages[1].content: got %s", userTextJSON)
	}

	mixed := got.Messages[2]
	if mixed.Role != "assistant" {
		t.Errorf("messages[2].role: got %q", mixed.Role)
	}
	if len(mixed.ThinkingBlocks) != 1 {
		t.Fatalf("messages[2].thinking_blocks len: want 1 got %d (issue #1153 item 5b)", len(mixed.ThinkingBlocks))
	}
	tb := mixed.ThinkingBlocks[0]
	if tb.Type != "thinking" || tb.Thinking != "I should call the weather tool." || tb.Signature != "sig_001" {
		t.Errorf("messages[2].thinking_blocks[0]: got %+v", tb)
	}
	if len(mixed.ToolCalls) != 1 {
		t.Fatalf("messages[2].tool_calls len: want 1 got %d", len(mixed.ToolCalls))
	}
	tc := mixed.ToolCalls[0]
	if tc.ID != "tu_01" || tc.Function.Name != "get_weather" {
		t.Errorf("messages[2].tool_calls[0]: got %+v", tc)
	}
	if tc.CacheControl == nil || tc.CacheControl.Type != "ephemeral" {
		t.Errorf("messages[2].tool_calls[0].cache_control: got %+v", tc.CacheControl)
	}
	mixedContentJSON, _ := json.Marshal(mixed.Content)
	var mixedParts []map[string]interface{}
	if err := json.Unmarshal(mixedContentJSON, &mixedParts); err != nil {
		t.Fatalf("messages[2].content is not an array (image or cache_control text part lost): %s", mixedContentJSON)
	}
	var sawText, sawImage bool
	for _, p := range mixedParts {
		switch p["type"] {
		case "text":
			if p["text"] == "Let me check." {
				sawText = true
			}
			cc, _ := p["cache_control"].(map[string]interface{})
			if cc == nil || cc["type"] != "ephemeral" {
				t.Errorf("messages[2].content text part cache_control: got %+v", p["cache_control"])
			}
		case "image_url":
			iu, _ := p["image_url"].(map[string]interface{})
			if iu != nil && iu["url"] == "data:image/png;base64,abc123" {
				sawImage = true
			}
		}
	}
	if !sawText {
		t.Errorf("messages[2].content: text part not found: %s", mixedContentJSON)
	}
	if !sawImage {
		t.Errorf("messages[2].content: image_url part not found, dropped by the tool-calls-plus-parts flatten branch (issue #1153 item 6): %s", mixedContentJSON)
	}

	toolResult := got.Messages[3]
	if toolResult.Role != "tool" {
		t.Errorf("messages[3].role: got %q", toolResult.Role)
	}
	if toolResult.ToolCallID != "tu_01" {
		t.Errorf("messages[3].tool_call_id: got %q", toolResult.ToolCallID)
	}
	if toolResult.CacheControl == nil || toolResult.CacheControl.Type != "ephemeral" {
		t.Errorf("messages[3].cache_control: got %+v", toolResult.CacheControl)
	}
	toolResultJSON, _ := json.Marshal(toolResult.Content)
	if string(toolResultJSON) != `"Sunny, 32C"` {
		t.Errorf("messages[3].content: got %s", toolResultJSON)
	}

	// The whole thing must still marshal cleanly end to end.
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("marshal OAIRequest: %v", err)
	}
}

// TestToOAIRequest_MaximalRequest_ToolChoiceNone_NotInverted is a second pass
// over the same maximal shape with tool_choice:"none" instead of "auto", kept
// separate because "none" and disable_parallel_tool_use=true cannot both be
// asserted from the same tool_choice object in one request.
func TestToOAIRequest_MaximalRequest_ToolChoiceNone_NotInverted(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:      "m",
		Messages:   []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:  5,
		ToolChoice: &anthropic.ToolChoice{Type: "none"},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}
	b, _ := json.Marshal(got.ToolChoice)
	if string(b) != `"none"` {
		t.Fatalf(`tool_choice: want "none" got %s`, b)
	}
}
