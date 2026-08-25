package anthropic_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

// --------------------------------------------------------------------------
// Request side: cache_control on every documented placement, plus the
// regression guard (no cache_control anywhere must change nothing) and the
// flattening trap the coordinator's research specifically called out: a
// typed content-block array carrying a cache breakpoint must survive
// translate_request.go as an array of typed blocks, never collapsed to a
// plain string (a plain string cannot carry a per-block cache_control, so
// collapsing it silently destroys the breakpoint the caller asked for).
// --------------------------------------------------------------------------

func TestToOAIRequest_CacheControl_Placements(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		check func(t *testing.T, got anthropic.OAIRequest)
	}{
		{
			name: "system block",
			raw: `{"model":"m","max_tokens":5,
				"system":[{"type":"text","text":"long instructions"},{"type":"text","text":"tail","cache_control":{"type":"ephemeral"}}],
				"messages":[{"role":"user","content":"hi"}]}`,
			check: func(t *testing.T, got anthropic.OAIRequest) {
				if got.Messages[0].Role != "system" {
					t.Fatalf("want first message role=system got %q", got.Messages[0].Role)
				}
				b, _ := json.Marshal(got.Messages[0].Content)
				var parts []map[string]interface{}
				if err := json.Unmarshal(b, &parts); err != nil {
					t.Fatalf("system content did not serialize as a block array: %s (err %v)", b, err)
				}
				if len(parts) != 2 {
					t.Fatalf("want 2 system blocks got %d: %s", len(parts), b)
				}
				if _, ok := parts[0]["cache_control"]; ok {
					t.Errorf("first system block must carry no cache_control, got %v", parts[0])
				}
				cc, ok := parts[1]["cache_control"].(map[string]interface{})
				if !ok {
					t.Fatalf("second system block missing cache_control: %v", parts[1])
				}
				if cc["type"] != "ephemeral" {
					t.Errorf("cache_control.type: want ephemeral got %v", cc["type"])
				}
			},
		},
		{
			name: "message content block, ttl 1h",
			raw: `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":[
				{"type":"text","text":"a huge cached document"},
				{"type":"text","text":"question","cache_control":{"type":"ephemeral","ttl":"1h"}}
			]}]}`,
			check: func(t *testing.T, got anthropic.OAIRequest) {
				b, _ := json.Marshal(got.Messages[0].Content)
				var parts []map[string]interface{}
				if err := json.Unmarshal(b, &parts); err != nil {
					t.Fatalf("message content did not serialize as a block array: %s (err %v)", b, err)
				}
				if len(parts) != 2 {
					t.Fatalf("want 2 message blocks got %d: %s", len(parts), b)
				}
				cc, ok := parts[1]["cache_control"].(map[string]interface{})
				if !ok {
					t.Fatalf("second message block missing cache_control: %v", parts[1])
				}
				if cc["type"] != "ephemeral" || cc["ttl"] != "1h" {
					t.Errorf("cache_control: want {ephemeral 1h} got %v", cc)
				}
			},
		},
		{
			name: "tool definition",
			raw: `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":"weather?"}],
				"tools":[{"name":"get_weather","input_schema":{},"cache_control":{"type":"ephemeral"}}]}`,
			check: func(t *testing.T, got anthropic.OAIRequest) {
				if len(got.Tools) != 1 {
					t.Fatalf("want 1 tool got %d", len(got.Tools))
				}
				if got.Tools[0].CacheControl == nil || got.Tools[0].CacheControl.Type != "ephemeral" {
					t.Errorf("tool cache_control: got %+v", got.Tools[0].CacheControl)
				}
				b, _ := json.Marshal(got.Tools[0])
				var m map[string]interface{}
				_ = json.Unmarshal(b, &m)
				if _, ok := m["cache_control"]; !ok {
					t.Errorf("tool JSON missing cache_control key entirely: %s", b)
				}
			},
		},
		{
			name: "request root",
			raw: `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":"hi"}],
				"cache_control":{"type":"ephemeral"}}`,
			check: func(t *testing.T, got anthropic.OAIRequest) {
				if got.CacheControl == nil || got.CacheControl.Type != "ephemeral" {
					t.Fatalf("root cache_control: got %+v", got.CacheControl)
				}
				b, _ := json.Marshal(got)
				var m map[string]interface{}
				_ = json.Unmarshal(b, &m)
				if _, ok := m["cache_control"]; !ok {
					t.Errorf("request JSON missing root cache_control key: %s", b)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req anthropic.MessagesRequest
			if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
				t.Fatalf("unmarshal request: %v", err)
			}
			got, err := anthropic.ToOAIRequest(req)
			if err != nil {
				t.Fatalf("ToOAIRequest: %v", err)
			}
			tc.check(t, got)
		})
	}
}

// TestToOAIRequest_CacheControl_SingleTextBlock_NotFlattenedToString is the
// flattening regression test: a message whose content is exactly one text
// block, which is the specific shape convertMessage collapses to a bare
// string for the common case, must NOT collapse when that one block carries
// a cache_control -- a string has nowhere to hang a breakpoint on, so
// flattening here would silently destroy the client's cache and then bill
// them full price for it.
func TestToOAIRequest_CacheControl_SingleTextBlock_NotFlattenedToString(t *testing.T) {
	raw := `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":[
		{"type":"text","text":"cache me","cache_control":{"type":"ephemeral"}}
	]}]}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}
	b, err := json.Marshal(got.Messages[0].Content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(b)), `"`) {
		t.Fatalf("content flattened to a bare string, cache_control lost: %s", b)
	}
	var parts []map[string]interface{}
	if err := json.Unmarshal(b, &parts); err != nil {
		t.Fatalf("content is not a block array: %s (err %v)", b, err)
	}
	if len(parts) != 1 || parts[0]["cache_control"] == nil {
		t.Fatalf("expected the sole block to keep its cache_control: %v", parts)
	}
}

// TestToOAIRequest_NoCacheControl_Unchanged is the regression guard: a
// request that carries no cache_control anywhere must serialize exactly as
// it did before cache_control support existed -- plain string content, no
// cache_control key anywhere in the output.
func TestToOAIRequest_NoCacheControl_Unchanged(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:     "claude-3-haiku",
		System:    anthropic.SystemField{Text: "Be concise."},
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "Hello"}}},
		MaxTokens: 100,
		Tools: []anthropic.Tool{
			{Name: "get_weather", InputSchema: json.RawMessage(`{}`)},
		},
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "cache_control") {
		t.Errorf("cache_control key leaked into a request that never set one: %s", b)
	}
	sysContent, _ := json.Marshal(got.Messages[0].Content)
	if string(sysContent) != `"Be concise."` {
		t.Errorf("system content: want plain string got %s", sysContent)
	}
}

func TestToOAIRequest_CacheControl_ToolResultBlock(t *testing.T) {
	raw := `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"tu_01","content":"Sunny","cache_control":{"type":"ephemeral"}}
	]}]}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}
	msg := got.Messages[0]
	if msg.CacheControl == nil || msg.CacheControl.Type != "ephemeral" {
		t.Errorf("tool_result message cache_control: got %+v", msg.CacheControl)
	}
}

func TestToOAIRequest_CacheControl_ToolUseBlock(t *testing.T) {
	raw := `{"model":"m","max_tokens":5,"messages":[{"role":"assistant","content":[
		{"type":"tool_use","id":"tu_01","name":"search","input":{"query":"q"},"cache_control":{"type":"ephemeral"}}
	]}]}`
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}
	if len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("want 1 tool call got %d", len(got.Messages[0].ToolCalls))
	}
	cc := got.Messages[0].ToolCalls[0].CacheControl
	if cc == nil || cc.Type != "ephemeral" {
		t.Errorf("tool_use cache_control: got %+v", cc)
	}
}

func TestToOAIRequest_SessionID_Passthrough(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model:     "m",
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens: 5,
		SessionID: "conv-abc-123",
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}
	if got.SessionID != "conv-abc-123" {
		t.Errorf("session_id: want conv-abc-123 got %q", got.SessionID)
	}
}

func TestToOAIRequest_SessionID_TruncatedAt256(t *testing.T) {
	long := strings.Repeat("a", 300)
	req := anthropic.MessagesRequest{
		Model:     "m",
		Messages:  []anthropic.Message{{Role: "user", Content: anthropic.MessageContent{Text: "hi"}}},
		MaxTokens: 5,
		SessionID: long,
	}
	got, err := anthropic.ToOAIRequest(req)
	if err != nil {
		t.Fatalf("ToOAIRequest: %v", err)
	}
	if len(got.SessionID) != 256 {
		t.Errorf("session_id length: want 256 got %d", len(got.SessionID))
	}
	if got.SessionID != long[:256] {
		t.Errorf("session_id: truncated value does not match the prefix of the original")
	}
}

// --------------------------------------------------------------------------
// Response side: the exclusive-shape echo contract Anthropic clients
// (Claude Code included) read cache savings from. OpenRouter's usage is
// inclusive (prompt_tokens already contains the cached and written tokens),
// so the fresh/uncached input count is a subtraction, never an addition.
// --------------------------------------------------------------------------

func TestFromOAIResponse_CacheTokens_NonStreaming(t *testing.T) {
	oai := anthropic.OAIResponse{
		ID: "chatcmpl-cache",
		Choices: []anthropic.OAIChoice{
			{FinishReason: "stop", Message: anthropic.OAIMsg{Role: "assistant", Content: "hi"}},
		},
		Usage: anthropic.OAIUsage{
			PromptTokens:     1000,
			CompletionTokens: 20,
			TotalTokens:      1020,
			PromptTokensDetails: &anthropic.OAIPromptTokensDetails{
				CachedTokens:     800,
				CacheWriteTokens: 50,
			},
		},
	}

	got := anthropic.FromOAIResponse(oai, "claude-3-haiku")

	if got.Usage.InputTokens != 150 {
		t.Errorf("input_tokens (fresh remainder): want 150 got %d", got.Usage.InputTokens)
	}
	if got.Usage.CacheReadInputTokens != 800 {
		t.Errorf("cache_read_input_tokens: want 800 got %d", got.Usage.CacheReadInputTokens)
	}
	if got.Usage.CacheCreationInputTokens != 50 {
		t.Errorf("cache_creation_input_tokens: want 50 got %d", got.Usage.CacheCreationInputTokens)
	}
	if got.Usage.OutputTokens != 20 {
		t.Errorf("output_tokens: want 20 got %d", got.Usage.OutputTokens)
	}

	b, err := json.Marshal(got.Usage)
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	if _, ok := m["cache_read_input_tokens"]; !ok {
		t.Errorf("wire usage missing cache_read_input_tokens key: %s", b)
	}
	if _, ok := m["cache_creation_input_tokens"]; !ok {
		t.Errorf("wire usage missing cache_creation_input_tokens key: %s", b)
	}
}

// TestFromOAIResponse_CacheTokens_ClampedNeverNegative guards the failure
// mode the research doc calls out explicitly: if cached+written tokens ever
// exceed the inclusive prompt_tokens total (a sign the upstream shape
// assumption broke), the client-facing input_tokens must clamp to zero, never
// go negative.
func TestFromOAIResponse_CacheTokens_ClampedNeverNegative(t *testing.T) {
	oai := anthropic.OAIResponse{
		Usage: anthropic.OAIUsage{
			PromptTokens: 100,
			PromptTokensDetails: &anthropic.OAIPromptTokensDetails{
				CachedTokens:     90,
				CacheWriteTokens: 50,
			},
		},
	}
	got := anthropic.FromOAIResponse(oai, "m")
	if got.Usage.InputTokens != 0 {
		t.Errorf("input_tokens: want clamped 0 got %d", got.Usage.InputTokens)
	}
}

func TestFromOAIResponse_NoCacheDetails_InputTokensUnchanged(t *testing.T) {
	oai := anthropic.OAIResponse{
		Usage: anthropic.OAIUsage{PromptTokens: 10, CompletionTokens: 5},
	}
	got := anthropic.FromOAIResponse(oai, "m")
	if got.Usage.InputTokens != 10 {
		t.Errorf("input_tokens with no cache details: want unchanged 10 got %d", got.Usage.InputTokens)
	}
	if got.Usage.CacheReadInputTokens != 0 || got.Usage.CacheCreationInputTokens != 0 {
		t.Errorf("cache fields should be zero with no PromptTokensDetails: %+v", got.Usage)
	}
	b, _ := json.Marshal(got.Usage)
	if strings.Contains(string(b), "cache_read_input_tokens") || strings.Contains(string(b), "cache_creation_input_tokens") {
		t.Errorf("omitempty cache fields leaked into a response with no cache activity: %s", b)
	}
}

// --------------------------------------------------------------------------
// Streaming: the same exclusive-shape numbers, carried through the SSE
// translator's message_delta (the only point in this relay architecture
// where the terminal, authoritative usage is known -- see the doc comment on
// SSETranslator.emitMessageStart for why message_start cannot carry them).
// --------------------------------------------------------------------------

func TestSSETranslator_CacheTokensInMessageDelta(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-c","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,`+
			`"prompt_tokens_details":{"cached_tokens":800,"cache_write_tokens":50}}}`,
	)
	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec, "m")
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	events := parseSSEEvents(t, rec.Body.String())
	found := false
	for _, ev := range events {
		if ev["type"] != "message_delta" {
			continue
		}
		found = true
		usage, ok := ev["usage"].(map[string]interface{})
		if !ok {
			t.Fatalf("message_delta has no usage object")
		}
		if usage["cache_read_input_tokens"].(float64) != 800 {
			t.Errorf("cache_read_input_tokens: want 800 got %v", usage["cache_read_input_tokens"])
		}
		if usage["cache_creation_input_tokens"].(float64) != 50 {
			t.Errorf("cache_creation_input_tokens: want 50 got %v", usage["cache_creation_input_tokens"])
		}
		if usage["output_tokens"].(float64) != 20 {
			t.Errorf("output_tokens: want 20 got %v", usage["output_tokens"])
		}
	}
	if !found {
		t.Fatal("no message_delta event observed")
	}
}
