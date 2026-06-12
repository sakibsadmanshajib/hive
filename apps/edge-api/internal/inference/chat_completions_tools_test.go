package inference

import (
	"context"
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
// with tools are also rejected when no tool-capable route exists (nil routing = fail closed).
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

// stubRoutingClient is a test double for the routing probe in guardToolCapability.
type stubRoutingClient struct {
	result SelectRouteResult
	err    error
}

func (s *stubRoutingClient) SelectRoute(_ context.Context, _ SelectRouteInput) (SelectRouteResult, error) {
	return s.result, s.err
}

// routingProber is the interface satisfied by RoutingClient and stubRoutingClient.
type routingProber interface {
	SelectRoute(ctx context.Context, input SelectRouteInput) (SelectRouteResult, error)
}

// TestFirstToolParam_Detection verifies that firstToolParam correctly identifies
// which parameter is present in a request.
func TestFirstToolParam_Detection(t *testing.T) {
	cases := []struct {
		name      string
		req       ChatCompletionRequest
		wantParam string
	}{
		{
			name:      "tools present",
			req:       ChatCompletionRequest{Tools: json.RawMessage(`[{"type":"function"}]`)},
			wantParam: "tools",
		},
		{
			name:      "tool_choice present",
			req:       ChatCompletionRequest{ToolChoice: json.RawMessage(`"auto"`)},
			wantParam: "tool_choice",
		},
		{
			name:      "response_format present",
			req:       ChatCompletionRequest{ResponseFormat: json.RawMessage(`{"type":"json_object"}`)},
			wantParam: "response_format",
		},
		{
			name:      "functions present",
			req:       ChatCompletionRequest{Functions: json.RawMessage(`[{"name":"f"}]`)},
			wantParam: "functions",
		},
		{
			name:      "function_call present",
			req:       ChatCompletionRequest{FunctionCall: json.RawMessage(`"auto"`)},
			wantParam: "function_call",
		},
		{
			name:      "empty tools array is present",
			req:       ChatCompletionRequest{Tools: json.RawMessage(`[]`)},
			wantParam: "tools",
		},
		{
			name:      "null tools is absent",
			req:       ChatCompletionRequest{Tools: json.RawMessage(`null`)},
			wantParam: "",
		},
		{
			name:      "no tool params",
			req:       ChatCompletionRequest{},
			wantParam: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstToolParam(&tc.req)
			if got != tc.wantParam {
				t.Errorf("firstToolParam() = %q, want %q", got, tc.wantParam)
			}
		})
	}
}

// TestChatCompletions_ToolsWithCapableRoute verifies that when a tool-capable
// route exists, the request is NOT blocked at the guard and proceeds to auth
// (returning 401 since no auth is configured in the test).
func TestChatCompletions_ToolsWithCapableRoute(t *testing.T) {
	// Stub routing client that always reports a capable route found.
	stub := &stubRoutingClient{
		result: SelectRouteResult{
			AliasID:          "gpt-4o",
			RouteID:          "route-capable",
			LiteLLMModelName: "route-capable",
			Provider:         "openrouter",
		},
		err: nil,
	}

	// Build an orchestrator with the stub routing client. The test validates that
	// the tools guard passes through and the request reaches the auth layer (401).
	orch := &Orchestrator{
		routing: newRoutingClientFromStub(stub),
	}
	h := NewHandler(orch)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Must NOT be 400 (guard passed). Should reach auth layer (401) since no
	// Authorization header is set.
	if w.Code == http.StatusBadRequest {
		t.Fatalf("expected request to pass tools guard (not 400), got 400: %s", w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth layer), got %d: %s", w.Code, w.Body.String())
	}
}

// TestChatCompletions_ToolsWithNoCapableRoute verifies that when the routing
// probe returns an error (no capable route), the guard returns 400 with
// "unsupported_parameter" and the "param" field set.
func TestChatCompletions_ToolsWithNoCapableRoute(t *testing.T) {
	stub := &stubRoutingClient{
		err: &routingProbeError{msg: "routing: status 422: routing: no tool-capable route"},
	}

	orch := &Orchestrator{
		routing: newRoutingClientFromStub(stub),
	}
	h := NewHandler(orch)

	body := `{"model":"hive-basic","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object","properties":{}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for tools on incapable alias, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level error object")
	}

	code, _ := errObj["code"].(string)
	if code != "unsupported_parameter" {
		t.Errorf("expected code 'unsupported_parameter', got %q", code)
	}

	param, _ := errObj["param"].(string)
	if param != "tools" {
		t.Errorf("expected param 'tools', got %q", param)
	}

	// Provider-blind: no provider names in message.
	msg, _ := errObj["message"].(string)
	for _, forbidden := range []string{"openrouter", "OpenRouter", "groq", "Groq", "LiteLLM", "litellm"} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("error message leaks provider name %q: %s", forbidden, msg)
		}
	}
}

// TestChatCompletions_ToolsRoutingTransientError verifies that when the routing
// probe fails with a non-422 error (e.g. 500, network error), the guard returns
// 502 Bad Gateway rather than 400 unsupported_parameter, so the caller knows the
// failure is transient and retryable, not a permanent capability mismatch.
func TestChatCompletions_ToolsRoutingTransientError(t *testing.T) {
	stub := &stubRoutingClientWithStatus{
		statusCode: http.StatusInternalServerError,
		body:       `{"error":"internal routing failure"}`,
	}

	orch := &Orchestrator{
		routing: newRoutingClientFromStatusStub(stub),
	}
	h := NewHandler(orch)

	body := `{"model":"hive-basic","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object","properties":{}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for transient routing failure, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level error object")
	}
	code, _ := errObj["code"].(string)
	if code != "routing_error" {
		t.Errorf("expected code 'routing_error', got %q", code)
	}
}

// routingProbeError is a test error type.
type routingProbeError struct{ msg string }

func (e *routingProbeError) Error() string { return e.msg }

// stubRoutingClientWithStatus returns a fixed HTTP status and body from the stub server.
type stubRoutingClientWithStatus struct {
	statusCode int
	body       string
}

// newRoutingClientFromStub builds a RoutingClient whose underlying transport
// is replaced by the stub via an in-process httptest.Server. This lets us
// inject fake responses without modifying RoutingClient internals.
func newRoutingClientFromStub(stub routingProber) *RoutingClient {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in SelectRouteInput
		json.NewDecoder(r.Body).Decode(&in)
		result, err := stub.SelectRoute(r.Context(), in)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}))
	// Note: srv is not closed in tests; the test process reclaims it.
	return NewRoutingClient(srv.URL)
}

// newRoutingClientFromStatusStub builds a RoutingClient whose stub always
// returns the given HTTP status code and body, for testing error path handling.
func newRoutingClientFromStatusStub(stub *stubRoutingClientWithStatus) *RoutingClient {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(stub.statusCode)
		w.Write([]byte(stub.body))
	}))
	return NewRoutingClient(srv.URL)
}
