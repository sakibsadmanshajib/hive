package anthropic_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

func newAuthedRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		Role:     "member",
		Email:    "test@example.com",
	}))
	return req
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: want 405 got %d", rec.Code)
	}
}

func TestHandler_MissingUser(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401 got %d", rec.Code)
	}
}

func TestHandler_NoTenant(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), Role: "member"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: want 403 got %d", rec.Code)
	}
}

func TestHandler_NoRole(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: uuid.New(), Role: "guest"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: want 403 got %d", rec.Code)
	}
}

func TestHandler_BadJSON(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := newAuthedRequest(t, `{not valid json}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400 got %d", rec.Code)
	}
}

func TestHandler_MissingModel(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := newAuthedRequest(t, `{"messages":[{"role":"user","content":"hi"}],"max_tokens":5}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400 got %d", rec.Code)
	}
}

func TestHandler_MissingMessages(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := newAuthedRequest(t, `{"model":"m","max_tokens":5}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400 got %d", rec.Code)
	}
}

func TestHandler_NonStreamHappyPath(t *testing.T) {
	oaiResp := map[string]interface{}{
		"id":    "chatcmpl-test",
		"model": "openrouter/anthropic/claude-3-haiku", // upstream route id
		"choices": []map[string]interface{}{
			{"index": 0, "finish_reason": "stop", "message": map[string]interface{}{"role": "assistant", "content": "Hello!"}},
		},
		"usage": map[string]interface{}{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(oaiResp)
	}))
	defer upstream.Close()

	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: upstream.URL, LiteLLMKey: "k"})
	req := newAuthedRequest(t, `{"model":"claude-3-haiku","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got anthropic.MessagesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Type != "message" || got.Role != "assistant" {
		t.Errorf("type/role: %q/%q", got.Type, got.Role)
	}
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason: want end_turn got %q", got.StopReason)
	}
	// Finding 2: model echoed back must be client alias.
	if got.Model != "claude-3-haiku" {
		t.Errorf("model: want claude-3-haiku got %q", got.Model)
	}
	if strings.Contains(got.Model, "openrouter") {
		t.Errorf("upstream route id leaked in model: %q", got.Model)
	}
	if len(got.Content) == 0 || got.Content[0].Text != "Hello!" {
		t.Errorf("content: %+v", got.Content)
	}
}

func TestHandler_StreamHappyPath(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-s","model":"route-x","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"chatcmpl-s","model":"route-x","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-s","model":"route-x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`,
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, stream)
	}))
	defer upstream.Close()

	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: upstream.URL, LiteLLMKey: "k"})
	req := newAuthedRequest(t, `{"model":"my-alias","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"stream":true}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "message_start") {
		t.Error("stream missing message_start")
	}
	if !strings.Contains(body, "message_stop") {
		t.Error("stream missing message_stop")
	}
	// Finding 2: model in message_start must be client alias.
	if strings.Contains(body, "route-x") {
		t.Error("upstream model route-x leaked in stream")
	}
	if !strings.Contains(body, "my-alias") {
		t.Error("client alias my-alias not present in stream")
	}
}

// Finding 8: provider-blind test uses the full stableErrorLeakPatterns set.
func TestHandler_UpstreamError_IsProviderBlind(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"groq rate limit hit via openrouter/auto; litellm route-fast; anthropic backend; openai upstream"}}`)
	}))
	defer upstream.Close()

	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: upstream.URL, LiteLLMKey: "k"})
	req := newAuthedRequest(t, `{"model":"my-alias","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status: want 429 got %d", rec.Code)
	}
	body := strings.ToLower(rec.Body.String())
	// Full stableErrorLeakPatterns provider name set from errors/codes.go.
	leakTerms := []string{
		"openai", "anthropic", "openrouter", "groq", "ollama", "vllm", "sglang",
		"nim", "litellm", "google", "gemini", "mistral", "cohere", "cerebras",
		"deepseek", "xai", "together", "fireworks", "replicate", "perplexity",
		"route-",
	}
	for _, term := range leakTerms {
		if strings.Contains(body, term) {
			t.Errorf("provider-blind violation: %q leaked in response body", term)
		}
	}
}

func TestHandler_CountTokens(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"Hello world"}],"max_tokens":5}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got anthropic.CountTokensResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.InputTokens <= 0 {
		t.Errorf("input_tokens: want > 0 got %d", got.InputTokens)
	}
}

func TestHandler_CountTokens_BadJSON(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{LiteLLMURL: "http://unused", LiteLLMKey: "k"})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{bad}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want 400 got %d", rec.Code)
	}
}

func TestAPIKeyNormalizer_RewritesXApiKey(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	h := anthropic.APIKeyNormalizer(inner)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "hk_test123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if captured != "Bearer hk_test123" {
		t.Errorf("Authorization: want %q got %q", "Bearer hk_test123", captured)
	}
}

func TestAPIKeyNormalizer_PreservesExistingAuthorization(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	h := anthropic.APIKeyNormalizer(inner)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer existing_token")
	req.Header.Set("x-api-key", "hk_other")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if captured != "Bearer existing_token" {
		t.Errorf("Authorization: want existing_token got %q", captured)
	}
}

func TestAPIKeyNormalizer_NoKey_PassesThrough(t *testing.T) {
	var captured string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	h := anthropic.APIKeyNormalizer(inner)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if captured != "" {
		t.Errorf("Authorization: want empty got %q", captured)
	}
}
