package anthropic_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

func TestFromOAIResponse_SimpleText(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "chatcmpl-abc",
		Model: "openrouter/anthropic/claude-3-haiku", // upstream route id -- must never reach client
		Choices: []anthropic.OAIChoice{
			{
				FinishReason: "stop",
				Message:      anthropic.OAIMsg{Role: "assistant", Content: "Hello, world!"},
			},
		},
		Usage: anthropic.OAIUsage{PromptTokens: 10, CompletionTokens: 5},
	}

	got := anthropic.FromOAIResponse(oai, "claude-3-haiku")

	if got.Type != "message" {
		t.Errorf("type: want message got %q", got.Type)
	}
	if got.Role != "assistant" {
		t.Errorf("role: want assistant got %q", got.Role)
	}
	// Model must be client alias, never the upstream route id.
	if got.Model != "claude-3-haiku" {
		t.Errorf("model: want claude-3-haiku got %q", got.Model)
	}
	if strings.Contains(got.Model, "openrouter") || strings.Contains(got.Model, "/") {
		t.Errorf("model leaks upstream route id: %q", got.Model)
	}
	if len(got.ID) < 4 || got.ID[:4] != "msg_" {
		t.Errorf("id prefix: want msg_ got %q", got.ID)
	}
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason: want end_turn got %q", got.StopReason)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "text" || got.Content[0].Text != "Hello, world!" {
		t.Errorf("content: %+v", got.Content)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 5 {
		t.Errorf("usage: %+v", got.Usage)
	}
}

// Finding 2: client alias is always echoed; upstream route id never leaks.
func TestFromOAIResponse_ModelAliasEchoed_RouteIdNeverLeaks(t *testing.T) {
	providerRouteIDs := []string{
		"openrouter/anthropic/claude-3-haiku",
		"groq/llama-3.1-8b",
		"route-openrouter-fast",
		"litellm/claude-haiku",
	}
	clientAlias := "my-model-alias"
	for _, upstreamModel := range providerRouteIDs {
		oai := anthropic.OAIResponse{
			ID:    "chatcmpl-x",
			Model: upstreamModel,
			Choices: []anthropic.OAIChoice{
				{Message: anthropic.OAIMsg{Role: "assistant", Content: "hi"}},
			},
		}
		got := anthropic.FromOAIResponse(oai, clientAlias)
		if got.Model != clientAlias {
			t.Errorf("upstream=%q: model want %q got %q", upstreamModel, clientAlias, got.Model)
		}
	}
}

// Finding 2: empty clientAlias falls back to resp.Model (graceful degradation).
func TestFromOAIResponse_EmptyAlias_FallsBackToRespModel(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID:    "chatcmpl-x",
		Model: "some-model",
		Choices: []anthropic.OAIChoice{
			{Message: anthropic.OAIMsg{Role: "assistant", Content: "hi"}},
		},
	}
	got := anthropic.FromOAIResponse(oai, "")
	if got.Model != "some-model" {
		t.Errorf("model: want some-model got %q", got.Model)
	}
}

func TestFromOAIResponse_FinishReasonMapping(t *testing.T) {
	cases := []struct{ oai, want string }{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"content_filter", "refusal"},
		{"unknown", "end_turn"},
		{"", "end_turn"},
	}
	for _, tc := range cases {
		oai := anthropic.OAIResponse{
			ID:    "x",
			Model: "m",
			Choices: []anthropic.OAIChoice{
				{FinishReason: tc.oai, Message: anthropic.OAIMsg{Role: "assistant", Content: "hi"}},
			},
		}
		got := anthropic.FromOAIResponse(oai, "m")
		if got.StopReason != tc.want {
			t.Errorf("finish_reason=%q: want %q got %q", tc.oai, tc.want, got.StopReason)
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
					Role: "assistant",
					ToolCalls: []anthropic.OAIToolCall{
						{ID: "call_01", Type: "function", Function: anthropic.OAIFunctionCall{Name: "get_weather", Arguments: `{"city":"Dhaka"}`}},
					},
				},
			},
		},
		Usage: anthropic.OAIUsage{PromptTokens: 20, CompletionTokens: 10},
	}
	got := anthropic.FromOAIResponse(oai, "m")
	if got.StopReason != "tool_use" {
		t.Errorf("stop_reason: want tool_use got %q", got.StopReason)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "tool_use" {
		t.Fatalf("content: %+v", got.Content)
	}
	block := got.Content[0]
	if block.ID != "call_01" || block.Name != "get_weather" {
		t.Errorf("block: %+v", block)
	}
	var input map[string]string
	if err := json.Unmarshal(block.Input, &input); err != nil || input["city"] != "Dhaka" {
		t.Errorf("input: %v raw=%s", err, block.Input)
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
					Content: "Let me check.",
					ToolCalls: []anthropic.OAIToolCall{
						{ID: "call_02", Type: "function", Function: anthropic.OAIFunctionCall{Name: "lookup", Arguments: `{"q":"test"}`}},
					},
				},
			},
		},
	}
	got := anthropic.FromOAIResponse(oai, "m")
	if len(got.Content) != 2 {
		t.Fatalf("content len: want 2 got %d", len(got.Content))
	}
	if got.Content[0].Type != "text" || got.Content[1].Type != "tool_use" {
		t.Errorf("block types: %v %v", got.Content[0].Type, got.Content[1].Type)
	}
}

func TestFromOAIResponse_EmptyChoices(t *testing.T) {
	oai := anthropic.OAIResponse{ID: "x", Model: "m"}
	got := anthropic.FromOAIResponse(oai, "m")
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason: want end_turn got %q", got.StopReason)
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
	got := anthropic.FromOAIResponse(oai, "m")
	if got.ID != "msg_already" {
		t.Errorf("id: want msg_already got %q", got.ID)
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
						{ID: "call_bad", Type: "function", Function: anthropic.OAIFunctionCall{Name: "fn", Arguments: `not json`}},
					},
				},
			},
		},
	}
	got := anthropic.FromOAIResponse(oai, "m")
	if len(got.Content) != 1 || got.Content[0].Type != "tool_use" {
		t.Fatalf("content: %+v", got.Content)
	}
	if len(got.Content[0].Input) == 0 {
		t.Error("input should not be empty on fallback")
	}
}
