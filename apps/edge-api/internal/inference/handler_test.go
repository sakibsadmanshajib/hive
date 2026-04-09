package inference

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandler_UnknownPath(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	req := httptest.NewRequest(http.MethodPost, "/v1/unknown", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_ChatCompletions_MissingModel(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	body := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var errResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &errResp)
	errObj, ok := errResp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "model") {
		t.Fatalf("expected error about model, got: %s", msg)
	}
}

func TestHandler_Completions_MissingModel(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	body := `{"prompt":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ChatCompletions_InvalidBody(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_Responses_MissingModel(t *testing.T) {
	// The responses endpoint is now live. A request missing the model field
	// returns 400 (not the old 501 placeholder).
	h := NewHandler(&Orchestrator{})
	body := `{"input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (endpoint live, missing model), got %d", w.Code)
	}
}

func TestHandler_Embeddings_MissingModel(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	body := `{"input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ChatCompletions_StreamLive(t *testing.T) {
	// Streaming is now implemented. A streaming request with no auth header
	// returns 401 (not the old 501 placeholder).
	h := NewHandler(&Orchestrator{})
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// 401 means the request reached the auth layer — streaming is live.
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (streaming live, no auth), got %d", w.Code)
	}
}

func TestNormalizeChatCompletion(t *testing.T) {
	input := `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"route-openrouter-default","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

	normalized, usage, err := normalizeChatCompletion([]byte(input), "gpt-4o")
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	var resp ChatCompletionResponse
	json.Unmarshal(normalized, &resp)

	if resp.Model != "gpt-4o" {
		t.Fatalf("expected model 'gpt-4o', got '%s'", resp.Model)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("expected object 'chat.completion', got '%s'", resp.Object)
	}
	if usage == nil || usage.TotalTokens != 15 {
		t.Fatalf("expected total_tokens 15, got %+v", usage)
	}
}

func TestNormalizeCompletion(t *testing.T) {
	input := `{"id":"cmpl-123","object":"text_completion","created":1234567890,"model":"route-openrouter-default","choices":[{"text":"world","index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`

	normalized, usage, err := normalizeCompletion([]byte(input), "gpt-3.5-turbo-instruct")
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	var resp CompletionResponse
	json.Unmarshal(normalized, &resp)

	if resp.Model != "gpt-3.5-turbo-instruct" {
		t.Fatalf("expected model 'gpt-3.5-turbo-instruct', got '%s'", resp.Model)
	}
	if resp.Object != "text_completion" {
		t.Fatalf("expected object 'text_completion', got '%s'", resp.Object)
	}
	if usage == nil || usage.TotalTokens != 8 {
		t.Fatalf("expected total_tokens 8, got %+v", usage)
	}
}
