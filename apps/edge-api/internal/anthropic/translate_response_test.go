package anthropic_test

import (
	"encoding/json"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

func TestFromOAIResponse_SimpleText(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "chatcmpl-abc",
		Model: "claude-3-haiku",
		Choices: []anthropic.OAIChoice{
			{
				Index:        0,
				FinishReason: "stop",
				Message: anthropic.OAIMsg{
					Role:    "assistant",
					Content: "Hello, world!",
				},
			},
		},
		Usage: anthropic.OAIUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}

	got := anthropic.FromOAIResponse(oai)

	if got.Type != "message" {
		t.Errorf("type: want %q got %q", "message", got.Type)
	}
	if got.Role != "assistant" {
		t.Errorf("role: want %q got %q", "assistant", got.Role)
	}
	if got.Model != "claude-3-haiku" {
		t.Errorf("model: want %q got %q", "claude-3-haiku", got.Model)
	}
	// ID must be prefixed with msg_
	if len(got.ID) < 4 || got.ID[:4] != "msg_" {
		t.Errorf("id prefix: want msg_ got %q", got.ID)
	}
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason: want %q got %q", "end_turn", got.StopReason)
	}
	if len(got.Content) != 1 {
		t.Fatalf("content len: want 1 got %d", len(got.Content))
	}
	if got.Content[0].Type != "text" {
		t.Errorf("content[0].type: want %q got %q", "text", got.Content[0].Type)
	}
	if got.Content[0].Text != "Hello, world!" {
		t.Errorf("content[0].text: want %q got %q", "Hello, world!", got.Content[0].Text)
	}
	if got.Usage.InputTokens != 10 {
		t.Errorf("usage.input_tokens: want 10 got %d", got.Usage.InputTokens)
	}
	if got.Usage.OutputTokens != 5 {
		t.Errorf("usage.output_tokens: want 5 got %d", got.Usage.OutputTokens)
	}
}

func TestFromOAIResponse_FinishReasonMapping(t *testing.T) {
	cases := []struct {
		oaiReason  string
		wantReason string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"content_filter", "refusal"},
		{"unknown", "end_turn"},
		{"", "end_turn"},
	}

	for _, tc := range cases {
		oai := anthropic.OAIResponse{
			ID:    "chatcmpl-xyz",
			Model: "m",
			Choices: []anthropic.OAIChoice{
				{FinishReason: tc.oaiReason, Message: anthropic.OAIMsg{Role: "assistant", Content: "hi"}},
			},
		}
		got := anthropic.FromOAIResponse(oai)
		if got.StopReason != tc.wantReason {
			t.Errorf("finish_reason=%q: want stop_reason=%q got %q", tc.oaiReason, tc.wantReason, got.StopReason)
		}
	}
}

func TestFromOAIResponse_WithToolCalls(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "chatcmpl-tool",
		Model: "m",
		Choices: []anthropic.OAIChoice{
			{
				FinishReason: "tool_calls",
				Message: anthropic.OAIMsg{
					Role:    "assistant",
					Content: "",
					ToolCalls: []anthropic.OAIToolCall{
						{
							ID:   "call_01",
							Type: "function",
							Function: anthropic.OAIFunctionCall{
								Name:      "get_weather",
								Arguments: `{"city":"Dhaka"}`,
							},
						},
					},
				},
			},
		},
		Usage: anthropic.OAIUsage{PromptTokens: 20, CompletionTokens: 10},
	}

	got := anthropic.FromOAIResponse(oai)

	if got.StopReason != "tool_use" {
		t.Errorf("stop_reason: want %q got %q", "tool_use", got.StopReason)
	}
	// Empty content field -> no text block; one tool_use block.
	if len(got.Content) != 1 {
		t.Fatalf("content len: want 1 got %d", len(got.Content))
	}
	block := got.Content[0]
	if block.Type != "tool_use" {
		t.Errorf("block type: want %q got %q", "tool_use", block.Type)
	}
	if block.ID != "call_01" {
		t.Errorf("block id: want %q got %q", "call_01", block.ID)
	}
	if block.Name != "get_weather" {
		t.Errorf("block name: want %q got %q", "get_weather", block.Name)
	}
	// Input must be valid JSON matching original arguments.
	var input map[string]string
	if err := json.Unmarshal(block.Input, &input); err != nil {
		t.Fatalf("input json: %v", err)
	}
	if input["city"] != "Dhaka" {
		t.Errorf("input city: want %q got %q", "Dhaka", input["city"])
	}
}

func TestFromOAIResponse_TextAndToolCalls(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "chatcmpl-multi",
		Model: "m",
		Choices: []anthropic.OAIChoice{
			{
				FinishReason: "tool_calls",
				Message: anthropic.OAIMsg{
					Role:    "assistant",
					Content: "Let me check that for you.",
					ToolCalls: []anthropic.OAIToolCall{
						{
							ID:   "call_02",
							Type: "function",
							Function: anthropic.OAIFunctionCall{
								Name:      "lookup",
								Arguments: `{"q":"test"}`,
							},
						},
					},
				},
			},
		},
	}

	got := anthropic.FromOAIResponse(oai)

	// Should have 2 blocks: text + tool_use.
	if len(got.Content) != 2 {
		t.Fatalf("content len: want 2 got %d", len(got.Content))
	}
	if got.Content[0].Type != "text" {
		t.Errorf("block[0] type: want %q got %q", "text", got.Content[0].Type)
	}
	if got.Content[1].Type != "tool_use" {
		t.Errorf("block[1] type: want %q got %q", "tool_use", got.Content[1].Type)
	}
}

func TestFromOAIResponse_EmptyChoices(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "chatcmpl-empty",
		Model: "m",
	}
	got := anthropic.FromOAIResponse(oai)
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason: want %q got %q", "end_turn", got.StopReason)
	}
}

func TestFromOAIResponse_IDAlreadyPrefixed(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "msg_already",
		Model: "m",
		Choices: []anthropic.OAIChoice{
			{Message: anthropic.OAIMsg{Role: "assistant", Content: "hi"}},
		},
	}
	got := anthropic.FromOAIResponse(oai)
	// Must not double-prefix.
	if got.ID != "msg_already" {
		t.Errorf("id: want %q got %q", "msg_already", got.ID)
	}
}

func TestFromOAIResponse_ToolArgumentsInvalidJSON(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "chatcmpl-bad",
		Model: "m",
		Choices: []anthropic.OAIChoice{
			{
				FinishReason: "tool_calls",
				Message: anthropic.OAIMsg{
					Role: "assistant",
					ToolCalls: []anthropic.OAIToolCall{
						{
							ID:   "call_bad",
							Type: "function",
							Function: anthropic.OAIFunctionCall{
								Name:      "fn",
								Arguments: `not json`,
							},
						},
					},
				},
			},
		},
	}
	got := anthropic.FromOAIResponse(oai)
	// Should still produce a tool_use block (with fallback input), never panic.
	if len(got.Content) != 1 {
		t.Fatalf("content len: want 1 got %d", len(got.Content))
	}
	if got.Content[0].Type != "tool_use" {
		t.Errorf("block type: want tool_use got %q", got.Content[0].Type)
	}
	if len(got.Content[0].Input) == 0 {
		t.Error("input should not be empty even on fallback")
	}
}
