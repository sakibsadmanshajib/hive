package anthropic_test

import (
	"encoding/json"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

func TestToOAIRequest_SimpleTextMessage(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model: "claude-3-haiku",
		Messages: []anthropic.Message{
			{Role: "user", Content: anthropic.MessageContent{Text: "Hello"}},
		},
		MaxTokens: 100,
	}

	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "claude-3-haiku" {
		t.Errorf("model: want %q got %q", "claude-3-haiku", got.Model)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages len: want 1 got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "user" {
		t.Errorf("role: want %q got %q", "user", got.Messages[0].Role)
	}
	if got.Messages[0].Content != "Hello" {
		t.Errorf("content: want %q got %v", "Hello", got.Messages[0].Content)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 100 {
		t.Errorf("max_tokens: want 100 got %v", got.MaxTokens)
	}
}

func TestToOAIRequest_SystemPrependsMessage(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:  "claude-3-haiku",
		System: anthropic.SystemField{Text: "Be concise."},
		Messages: []anthropic.Message{
			{Role: "user", Content: anthropic.MessageContent{Text: "Hi"}},
		},
		MaxTokens: 10,
	}

	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len: want 2 got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" {
		t.Errorf("first message role: want %q got %q", "system", got.Messages[0].Role)
	}
	if got.Messages[0].Content != "Be concise." {
		t.Errorf("system content: want %q got %v", "Be concise.", got.Messages[0].Content)
	}
}

func TestToOAIRequest_SystemBlocks(t *testing.T) {
	raw := `{
		"model":"m","max_tokens":5,
		"system":[{"type":"text","text":"Part1"},{"type":"text","text":"Part2"}],
		"messages":[{"role":"user","content":"hi"}]
	}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.System.Text != "Part1Part2" {
		t.Errorf("system: want %q got %q", "Part1Part2", req.System.Text)
	}
}

func TestToOAIRequest_StopSequences(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:         "m",
		Messages:      []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:     5,
		StopSequences: []string{"END", "\n"},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Stop) != 2 || got.Stop[0] != "END" || got.Stop[1] != "\n" {
		t.Errorf("stop: want [END \\n] got %v", got.Stop)
	}
}

func TestToOAIRequest_ToolChoice_Auto(t *testing.T) {
	tc := &anthropic.ToolChoice{Type: "auto"}
	req := anthropic.MessagesRequest{
		Model:      "m",
		Messages:   []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:  5,
		ToolChoice: tc,
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ToolChoice != "auto" {
		t.Errorf("tool_choice: want %q got %v", "auto", got.ToolChoice)
	}
}

func TestToOAIRequest_ToolChoice_Any(t *testing.T) {
	tc := &anthropic.ToolChoice{Type: "any"}
	req := anthropic.MessagesRequest{
		Model:      "m",
		Messages:   []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:  5,
		ToolChoice: tc,
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ToolChoice != "required" {
		t.Errorf("tool_choice: want %q got %v", "required", got.ToolChoice)
	}
}

func TestToOAIRequest_ToolChoice_Named(t *testing.T) {
	tc := &anthropic.ToolChoice{Type: "tool", Name: "get_weather"}
	req := anthropic.MessagesRequest{
		Model:      "m",
		Messages:   []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:  5,
		ToolChoice: tc,
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := got.ToolChoice.(map[string]interface{})
	if !ok {
		t.Fatalf("tool_choice type: want map got %T", got.ToolChoice)
	}
	if m["type"] != "function" {
		t.Errorf("tool_choice.type: want %q got %v", "function", m["type"])
	}
	fn, _ := m["function"].(map[string]string)
	if fn["name"] != "get_weather" {
		t.Errorf("tool_choice.function.name: want %q got %v", "get_weather", fn["name"])
	}
}

func TestToOAIRequest_Tools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)
	req := anthropic.MessagesRequest{
		Model:     "m",
		MaxTokens: 5,
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "weather?"}}},
		Tools: []anthropic.Tool{
			{Name: "get_weather", Description: "Get weather", InputSchema: schema},
		},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools len: want 1 got %d", len(got.Tools))
	}
	if got.Tools[0].Type != "function" {
		t.Errorf("tool type: want %q got %q", "function", got.Tools[0].Type)
	}
	if got.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tool name: want %q got %q", "get_weather", got.Tools[0].Function.Name)
	}
}

func TestToOAIRequest_ImageContentBlock(t *testing.T) {
	blocks := []anthropic.ContentBlock{
		{Type: "text", Text: "Describe this image"},
		{
			Type: "image",
			Source: &anthropic.ImageSource{
				Type:      "base64",
				MediaType: "image/png",
				Data:      "abc123",
			},
		},
	}
	req := anthropic.MessagesRequest{
		Model:     "m",
		MaxTokens: 50,
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Blocks: blocks}}},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts, ok := got.Messages[0].Content.([]anthropic.OAIContentPart)
	if !ok {
		t.Fatalf("content type: want []OAIContentPart got %T", got.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts len: want 2 got %d", len(parts))
	}
	if parts[1].Type != "image_url" {
		t.Errorf("part[1] type: want %q got %q", "image_url", parts[1].Type)
	}
	if parts[1].ImageURL == nil {
		t.Fatal("image_url nil")
	}
	expected := "data:image/png;base64,abc123"
	if parts[1].ImageURL.URL != expected {
		t.Errorf("image URL: want %q got %q", expected, parts[1].ImageURL.URL)
	}
}

func TestToOAIRequest_ToolUseBlock(t *testing.T) {
	raw := `{
		"model":"m","max_tokens":5,
		"messages":[{
			"role":"assistant",
			"content":[{
				"type":"tool_use",
				"id":"tu_01","name":"search",
				"input":{"query":"Go lang"}
			}]
		}]
	}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := got.Messages[0]
	if msg.Role != "assistant" {
		t.Errorf("role: want %q got %q", "assistant", msg.Role)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len: want 1 got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "tu_01" {
		t.Errorf("tool_call id: want %q got %q", "tu_01", tc.ID)
	}
	if tc.Function.Name != "search" {
		t.Errorf("function name: want %q got %q", "search", tc.Function.Name)
	}
	// Arguments must be valid JSON
	var args interface{}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Errorf("arguments not valid JSON: %v, raw=%q", err, tc.Function.Arguments)
	}
}

func TestToOAIRequest_ToolResultBlock(t *testing.T) {
	raw := `{
		"model":"m","max_tokens":5,
		"messages":[{
			"role":"user",
			"content":[{
				"type":"tool_result",
				"tool_use_id":"tu_01",
				"content":"Sunny and 25°C"
			}]
		}]
	}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := got.Messages[0]
	if msg.Role != "tool" {
		t.Errorf("role: want %q got %q", "tool", msg.Role)
	}
	if msg.ToolCallID != "tu_01" {
		t.Errorf("tool_call_id: want %q got %q", "tu_01", msg.ToolCallID)
	}
	if msg.Content != "Sunny and 25°C" {
		t.Errorf("content: want %q got %v", "Sunny and 25°C", msg.Content)
	}
}

func TestToOAIRequest_EmptyModel_IsPreserved(t *testing.T) {
	// Model validation is the handler's responsibility; the translator passes
	// whatever is in the request so the handler can reject it uniformly.
	req := anthropic.MessagesRequest{
		Messages: []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "" {
		t.Errorf("model: want empty got %q", got.Model)
	}
}

func TestToOAIRequest_NilToolChoice_Omitted(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:     "m",
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens: 5,
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ToolChoice != nil {
		t.Errorf("tool_choice should be nil when not set, got %v", got.ToolChoice)
	}
}

func TestToOAIRequest_TemperatureAndTopP(t *testing.T) {
	temp := 0.7
	topP := 0.9
	req := anthropic.MessagesRequest{
		Model:       "m",
		Messages:    []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:   5,
		Temperature: &temp,
		TopP:        &topP,
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("temperature: want 0.7 got %v", got.Temperature)
	}
	if got.TopP == nil || *got.TopP != 0.9 {
		t.Errorf("top_p: want 0.9 got %v", got.TopP)
	}
}
