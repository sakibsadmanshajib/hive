package inference

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestChatCompletions_ToolsRejected verifies that requests carrying tools,
// tool_choice, response_format, parallel_tool_calls, functions, or
// function_call fields receive a 400 with an "unsupported_parameter" error
// code (issue #118 short-term fix).
//
// The handler must NOT silently drop these fields and forward to LiteLLM,
// because the backing provider may ignore them and return a plain content
// response, causing silent behavioural divergence for SDK consumers.
func TestChatCompletions_ToolsRejected(t *testing.T) {
	h := NewHandler(&Orchestrator{})

	cases := []struct {
		name string
		body string
	}{
		{
			name: "tools field rejected",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{}}}}]}`,
		},
		{
			name: "tool_choice field rejected",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object","properties":{}}}}],"tool_choice":"auto"}`,
		},
		{
			name: "response_format field rejected",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`,
		},
		{
			name: "parallel_tool_calls field rejected",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object","properties":{}}}}],"parallel_tool_calls":true}`,
		},
		{
			name: "functions (legacy) field rejected",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"functions":[{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{}}}]}`,
		},
		{
			name: "function_call (legacy) field rejected",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"functions":[{"name":"f","description":"d","parameters":{"type":"object","properties":{}}}],"function_call":"auto"}`,
		},
		{
			// Empty array must be treated as present (non-null), not silently ignored.
			// A client sending `"tools": []` has explicitly opted in to the tools
			// parameter and must receive the same 400 as a non-empty tools array.
			name: "tools empty array rejected",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body: %s)", w.Code, w.Body.String())
			}

			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}

			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected top-level 'error' object, got: %v", resp)
			}

			code, _ := errObj["code"].(string)
			if code != "unsupported_parameter" {
				t.Errorf("expected code 'unsupported_parameter', got %q", code)
			}

			errType, _ := errObj["type"].(string)
			if errType != "invalid_request_error" {
				t.Errorf("expected type 'invalid_request_error', got %q", errType)
			}

			// Error message must not leak provider names (provider-blind rule).
			msg, _ := errObj["message"].(string)
			for _, forbidden := range []string{"OpenAI", "openai", "LiteLLM", "litellm", "openrouter", "OpenRouter", "groq", "Groq"} {
				if strings.Contains(msg, forbidden) {
					t.Errorf("error message leaks provider name %q: %s", forbidden, msg)
				}
			}
		})
	}
}

// TestChatCompletions_NoToolsPassesThrough verifies that a plain chat
// completion request (no tools/tool_choice/response_format) is NOT rejected
// by the tools guard and reaches the auth layer (returns 401 with no auth).
func TestChatCompletions_NoToolsPassesThrough(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// 401 = reached auth layer, i.e. tools guard did NOT block it.
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (plain request passes tools guard), got %d: %s", w.Code, w.Body.String())
	}
}

// TestChatCompletions_StreamWithToolsRejected verifies that streaming requests
// with tools are also rejected before they reach the streaming path.
func TestChatCompletions_StreamWithToolsRejected(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true,"tools":[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object","properties":{}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for streaming+tools, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object")
	}
	code, _ := errObj["code"].(string)
	if code != "unsupported_parameter" {
		t.Errorf("expected code 'unsupported_parameter', got %q", code)
	}
}
