package inference

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #1348: an invalid message role was forwarded to the provider, refused
// there, and answered to the caller as "hive-small is not available." with
// code upstream_error. The fault was entirely inside the caller payload, and
// the gateway can see that without asking any upstream.
//
// Assertions read the serialized envelope, which is what an SDK parses.
func TestMalformedMessagesRefusedBeforeDispatch(t *testing.T) {
	h := NewHandler(&Orchestrator{})

	cases := []struct {
		name      string
		body      string
		wantParam string
		wantCode  string
	}{
		{
			name:      "unknown role",
			body:      `{"model":"hive-small","messages":[{"role":"not-a-real-role","content":"hi"}]}`,
			wantParam: "messages[0].role",
			wantCode:  "invalid_value",
		},
		{
			name:      "unknown role on a later message",
			body:      `{"model":"hive-small","messages":[{"role":"user","content":"hi"},{"role":"robot","content":"hi"}]}`,
			wantParam: "messages[1].role",
			wantCode:  "invalid_value",
		},
		{
			name:      "role missing entirely",
			body:      `{"model":"hive-small","messages":[{"content":"hi"}]}`,
			wantParam: "messages[0].role",
			wantCode:  "invalid_value",
		},
		{
			name:      "empty messages array",
			body:      `{"model":"hive-small","messages":[]}`,
			wantParam: "messages",
			wantCode:  "empty_array",
		},
		{
			name:      "messages is not an array of objects",
			body:      `{"model":"hive-small","messages":["hi"]}`,
			wantParam: "messages",
			wantCode:  "invalid_type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			raw := w.Body.String()
			if strings.Contains(raw, "upstream_error") {
				t.Errorf("a malformed payload is labelled an upstream failure: %s", raw)
			}
			if strings.Contains(raw, "is not available") {
				t.Errorf("a malformed payload is answered with an availability verdict: %s", raw)
			}
			errObj := decodeErrorBody(t, w)
			if errType, _ := errObj["type"].(string); errType != "invalid_request_error" {
				t.Errorf("type = %q, want invalid_request_error", errType)
			}
			if param, _ := errObj["param"].(string); param != tc.wantParam {
				t.Errorf("param = %v, want %q", errObj["param"], tc.wantParam)
			}
			if code, _ := errObj["code"].(string); code != tc.wantCode {
				t.Errorf("code = %v, want %q", errObj["code"], tc.wantCode)
			}
			msg, _ := errObj["message"].(string)
			if msg == "" {
				t.Error("refusal carries no message for the caller to read")
			}
			for _, leak := range []string{"openai", "OpenAI", "litellm", "LiteLLM", "groq", "Groq", "openrouter", "OpenRouter"} {
				if strings.Contains(raw, leak) {
					t.Errorf("provider identity leaked into the refusal: %q in %s", leak, raw)
				}
			}
		})
	}
}

// TestValidMessageRolesStillAccepted is what keeps the guard from becoming a
// blanket refusal: every role the surface defines must pass it.
//
// These requests carry no API key, so they stop at the authorizer with a 401.
// That is the pass condition: reaching authorization at all proves the body
// cleared validation, and a validation refusal is a 400 naming a messages
// param, which is exactly what is asserted against.
func TestValidMessageRolesStillAccepted(t *testing.T) {
	h := NewHandler(&Orchestrator{})

	for _, role := range []string{"system", "developer", "user", "assistant", "tool", "function"} {
		t.Run(role, func(t *testing.T) {
			body := `{"model":"hive-small","messages":[{"role":"` + role + `","content":"hi"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code == http.StatusUnauthorized {
				return
			}
			if w.Code == http.StatusBadRequest {
				errObj := decodeErrorBody(t, w)
				if param, _ := errObj["param"].(string); strings.HasPrefix(param, "messages") {
					t.Fatalf("role %q was refused as malformed: %s", role, w.Body.String())
				}
			}
		})
	}
}
