package inference

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #1283 finding 2: n: 2 was accepted with HTTP 200 and answered with a
// single choice. Accept-and-silently-truncate is the one outcome the OpenAI
// contract does not allow: a caller has no way to tell a parameter that was
// honoured from one that was dropped. No route in this catalog can serve n > 1,
// so the honest answer is a declared 400.

// decodeErrorBody pulls the OpenAI error envelope out of a recorder.
func decodeErrorBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, w.Body.String())
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected a top-level 'error' object, got: %v", resp)
	}
	return errObj
}

func TestNGreaterThanOneRejected(t *testing.T) {
	h := NewHandler(&Orchestrator{})

	cases := []struct {
		name string
		path string
		body string
	}{
		{
			name: "chat completions n=2",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello in one word."}],"max_tokens":16,"n":2}`,
		},
		{
			name: "chat completions n=0 is not a valid choice count either",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"n":0}`,
		},
		{
			name: "legacy completions n=2",
			path: "/v1/completions",
			body: `{"model":"gpt-4o","prompt":"Say hello in one word.","max_tokens":16,"n":2}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			errObj := decodeErrorBody(t, w)
			if code, _ := errObj["code"].(string); code != "unsupported_parameter" {
				t.Errorf("code = %q, want \"unsupported_parameter\"", code)
			}
			if errType, _ := errObj["type"].(string); errType != "invalid_request_error" {
				t.Errorf("type = %q, want \"invalid_request_error\"", errType)
			}
			if param, _ := errObj["param"].(string); param != "n" {
				t.Errorf("param = %v, want \"n\": an SDK caller needs to know which field to drop", errObj["param"])
			}
			msg, _ := errObj["message"].(string)
			for _, forbidden := range []string{"OpenAI", "openai", "LiteLLM", "litellm", "openrouter", "OpenRouter", "groq", "Groq", "gemini", "Gemini"} {
				if strings.Contains(msg, forbidden) {
					t.Errorf("error message leaks provider identity %q: %s", forbidden, msg)
				}
			}
		})
	}
}

// TestNOneOrAbsentPassesThrough keeps the rejection narrow: the only shape
// refused is the one that cannot be served. A bare Orchestrator has no
// authorizer, so a request that clears validation ends at 401, which is proof
// it was never refused as a bad request.
func TestNOneOrAbsentPassesThrough(t *testing.T) {
	h := NewHandler(&Orchestrator{})

	cases := []struct {
		name string
		path string
		body string
	}{
		{"chat n=1", "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"n":1}`},
		{"chat n absent", "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`},
		{"chat n null", "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"n":null}`},
		{"completions n=1", "/v1/completions", `{"model":"gpt-4o","prompt":"hi","n":1}`},
		{"completions n absent", "/v1/completions", `{"model":"gpt-4o","prompt":"hi"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code == http.StatusBadRequest {
				t.Fatalf("status = 400 on a servable request: %s", w.Body.String())
			}
		})
	}
}
