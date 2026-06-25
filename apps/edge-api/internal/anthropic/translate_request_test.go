package anthropic_test

import (
	"encoding/json"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

func TestToOAIRequest_SimpleTextMessage(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:     "claude-3-haiku",
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "Hello"}}},
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
	b, _ := json.Marshal(got.Messages[0].Content)
	if string(b) != `"Hello"` {
		t.Errorf("content json: want %q got %s", `"Hello"`, b)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 100 {
		t.Errorf("max_tokens: want 100 got %v", got.MaxTokens)
	}
}

func TestToOAIRequest_SystemPrependsMessage(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:     "m",
		System:    anthropic.SystemField{Text: "Be concise."},
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "Hi"}}},
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
		t.Errorf("first role: want system got %q", got.Messages[0].Role)
	}
	b, _ := json.Marshal(got.Messages[0].Content)
	if string(b) != `"Be concise."` {
		t.Errorf("system content json: want %q got %s", `"Be concise."`, b)
	}
}

func TestToOAIRequest_SystemBlocks(t *testing.T) {
	raw := `{"model":"m","max_tokens":5,"system":[{"type":"text","text":"Part1"},{"type":"text","text":"Part2"}],"messages":[{"role":"user","content":"hi"}]}`
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
	if len(got.Stop) != 2 || got.Stop[0] != "END" {
		t.Errorf("stop: want [END \\n] got %v", got.Stop)
	}
}

func TestToOAIRequest_ToolChoice_Auto(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:      "m",
		Messages:   []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:  5,
		ToolChoice: &anthropic.ToolChoice{Type: "auto"},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ToolChoice == nil {
		t.Fatal("tool_choice nil")
	}
	b, _ := json.Marshal(got.ToolChoice)
	if string(b) != `"auto"` {
		t.Errorf("tool_choice json: want %q got %s", `"auto"`, b)
	}
}

func TestToOAIRequest_ToolChoice_Any(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:      "m",
		Messages:   []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:  5,
		ToolChoice: &anthropic.ToolChoice{Type: "any"},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(got.ToolChoice)
	if string(b) != `"required"` {
		t.Errorf("tool_choice json: want %q got %s", `"required"`, b)
	}
}

func TestToOAIRequest_ToolChoice_Named(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:      "m",
		Messages:   []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens:  5,
		ToolChoice: &anthropic.ToolChoice{Type: "tool", Name: "get_weather"},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(got.ToolChoice)
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal tool_choice: %v", err)
	}
	if m["type"] != "function" {
		t.Errorf("type: want function got %v", m["type"])
	}
	fn, _ := m["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Errorf("function.name: want get_weather got %v", fn["name"])
	}
}

func TestToOAIRequest_Tools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)
	req := anthropic.MessagesRequest{
		Model:     "m",
		MaxTokens: 5,
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "weather?"}}},
		Tools:     []anthropic.Tool{{Name: "get_weather", Description: "Get weather", InputSchema: schema}},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tools: %+v", got.Tools)
	}
}

func TestToOAIRequest_ImageContentBlock(t *testing.T) {
	blocks := []anthropic.ContentBlock{
		{Type: "text", Text: "Describe this"},
		{Type: "image", Source: &anthropic.ImageSource{Type: "base64", MediaType: "image/png", Data: "abc123"}},
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
	b, _ := json.Marshal(got.Messages[0].Content)
	var parts []map[string]interface{}
	if err := json.Unmarshal(b, &parts); err != nil {
		t.Fatalf("unmarshal parts: %v body=%s", err, b)
	}
	if len(parts) != 2 {
		t.Fatalf("parts len: want 2 got %d", len(parts))
	}
	if parts[1]["type"] != "image_url" {
		t.Errorf("part[1] type: want image_url got %v", parts[1]["type"])
	}
	iu, _ := parts[1]["image_url"].(map[string]interface{})
	if iu["url"] != "data:image/png;base64,abc123" {
		t.Errorf("image url: want data URI got %v", iu["url"])
	}
}

func TestToOAIRequest_ToolUseBlock(t *testing.T) {
	raw := `{"model":"m","max_tokens":5,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tu_01","name":"search","input":{"query":"Go lang"}}]}]}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := got.Messages[0]
	if msg.Role != "assistant" || len(msg.ToolCalls) != 1 {
		t.Fatalf("message: role=%q tool_calls=%d", msg.Role, len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "tu_01" || msg.ToolCalls[0].Function.Name != "search" {
		t.Errorf("tool_call: %+v", msg.ToolCalls[0])
	}
	var args interface{}
	if err := json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &args); err != nil {
		t.Errorf("arguments not valid JSON: %v", err)
	}
}

func TestToOAIRequest_ToolResultBlock(t *testing.T) {
	raw := `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_01","content":"Sunny"}]}]}`
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
		t.Errorf("role: want tool got %q", msg.Role)
	}
	if msg.ToolCallID != "tu_01" {
		t.Errorf("tool_call_id: want tu_01 got %q", msg.ToolCallID)
	}
	b, _ := json.Marshal(msg.Content)
	if string(b) != `"Sunny"` {
		t.Errorf("content: want %q got %s", `"Sunny"`, b)
	}
}

// Finding 6: tool_result preceded by other blocks must return an error.
func TestToOAIRequest_ToolResultPrecededByBlock_ReturnsError(t *testing.T) {
	raw := `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":[{"type":"text","text":"note"},{"type":"tool_result","tool_use_id":"tu_01","content":"ok"}]}]}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := anthropic.ToOAIRequest(req)
	if err == nil {
		t.Fatal("expected error for tool_result with preceding blocks, got nil")
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
		t.Errorf("tool_choice should be nil when not set")
	}
}

func TestToOAIRequest_TemperatureAndTopP(t *testing.T) {
	temp, topP := 0.7, 0.9
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

// OAIMessageContent marshals as plain string for text-only content.
func TestOAIMessageContent_MarshalJSON_Text(t *testing.T) {
	c := anthropic.OAIMessageContent{Text: "hello"}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"hello"` {
		t.Errorf("want %q got %s", `"hello"`, b)
	}
}

// OAIMessageContent marshals as array for multipart content.
func TestOAIMessageContent_MarshalJSON_Parts(t *testing.T) {
	c := anthropic.OAIMessageContent{
		Parts: []anthropic.OAIContentPart{
			{Type: "text", Text: "hi"},
			{Type: "image_url", ImageURL: &anthropic.OAIImageURL{URL: "data:image/png;base64,abc"}},
		},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var arr []interface{}
	if err := json.Unmarshal(b, &arr); err != nil || len(arr) != 2 {
		t.Errorf("expected 2-element array, got: %s", b)
	}
}

// OAIToolChoice marshals as string sentinel.
func TestOAIToolChoice_MarshalJSON_Sentinel(t *testing.T) {
	tc := anthropic.OAIToolChoice{Sentinel: "required"}
	b, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"required"` {
		t.Errorf("want %q got %s", `"required"`, b)
	}
}

// OAIToolChoice marshals as object for named function.
func TestOAIToolChoice_MarshalJSON_Named(t *testing.T) {
	tc := anthropic.OAIToolChoice{
		Named: &anthropic.OAINamedToolChoice{
			Type:     "function",
			Function: anthropic.OAINamedToolChoiceFunction{Name: "fn"},
		},
	}
	b, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["type"] != "function" {
		t.Errorf("type: want function got %v", m["type"])
	}
}
