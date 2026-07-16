package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// --- fakes ---

func fakeSelectRoute(model string, err error) RouteSelectFunc {
	return func(_ context.Context, _ string) (string, error) {
		return model, err
	}
}

// fakeDispatch returns a canned OpenAI-shaped chat completion response.
func fakeDispatch(statusCode int, respBody string, err error) ChatDispatchFunc {
	return func(_ context.Context, _ string, _ []byte) (*http.Response, error) {
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	}
}

const canned200Response = `{"id":"upstream-123","choices":[{"message":{"role":"assistant","content":"The answer is 42 [1]."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

func newChatTestHandler(store *fakeStore, embed *fakeEmbedder, records *[]auditRecord, route RouteSelectFunc, dispatch ChatDispatchFunc) *Handler {
	h := newTestHandler(store, embed, records)
	return h.WithChat(route, dispatch)
}

func chatReq(t *testing.T, body ChatRequest, tenantID uuid.UUID) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/chat", bytes.NewReader(raw))
	req = req.WithContext(userCtx(tenantID))
	return req
}

// --- tests ---

func TestHandleChat_HappyPath(t *testing.T) {
	store := newFakeStore()
	docID := uuid.New()
	chunkID := uuid.New()
	store.chunks = []ChunkRow{{ID: chunkID, DocumentID: docID, Content: "relevant content", Score: 0.1}}

	var audits []auditRecord
	h := newChatTestHandler(store, &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil),
		fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer?"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChatResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model != "hive-fast" {
		t.Errorf("expected model to echo the client alias, got %q", resp.Model)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "The answer is 42 [1]." {
		t.Errorf("unexpected choices: %+v", resp.Choices)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("expected usage passthrough, got %+v", resp.Usage)
	}
	if len(resp.Citations) != 1 || resp.Citations[0].DocumentID != docID.String() {
		t.Errorf("expected 1 citation for document %s, got %+v", docID, resp.Citations)
	}
	if strings.Contains(resp.ID, "upstream-123") {
		t.Errorf("response id must not pass through the raw upstream id: %q", resp.ID)
	}

	var sawQuery, sawChunk bool
	for _, a := range audits {
		switch a.Action {
		case "RAG_CHAT_QUERY":
			sawQuery = true
		case "RAG_CHUNK_RETRIEVED":
			sawChunk = true
		}
	}
	if !sawQuery {
		t.Error("RAG_CHAT_QUERY audit not emitted")
	}
	if !sawChunk {
		t.Error("RAG_CHUNK_RETRIEVED audit not emitted")
	}
}

func TestHandleChat_NoChunksFound_StillAnswers(t *testing.T) {
	store := newFakeStore() // no chunks
	var audits []auditRecord
	h := newChatTestHandler(store, &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil),
		fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "anything?"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even with no retrieved context, got %d: %s", w.Code, w.Body.String())
	}
	var resp ChatResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Citations) != 0 {
		t.Errorf("expected zero citations, got %+v", resp.Citations)
	}
}

func TestHandleChat_MissingUserMessage(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "system", Content: "you are an assistant"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing user message, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleChat_MissingModel(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing model, got %d", w.Code)
	}
}

func TestHandleChat_EmbedFail_ProviderBlind(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{fail: true}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	assertNoLeak(t, w.Body.String())
}

func TestHandleChat_ChatNotConfigured_Returns503(t *testing.T) {
	var audits []auditRecord
	h := newTestHandler(newFakeStore(), &fakeEmbedder{}, &audits) // no WithChat call

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when chat deps are unset, got %d", w.Code)
	}
}

func TestHandleChat_StreamingRejected(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for streaming request (deferred scope), got %d", w.Code)
	}
}

func TestHandleChat_RouteNotFound_Returns404(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("", ErrRouteNotFound), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "unknown-model",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown model alias, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleChat_RouteTransportError_ProviderBlind(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("", errors.New("dial tcp 10.0.0.5:443: connect: connection refused")),
		fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a non-200 status on routing failure, got 200")
	}
	assertNoLeak(t, w.Body.String())
}

func TestHandleChat_DispatchTransportError_ProviderBlind(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil),
		fakeDispatch(0, "", errors.New("dial tcp: connection refused to openrouter.ai")))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a non-200 status on dispatch transport failure, got 200")
	}
	assertNoLeak(t, w.Body.String())
}

func TestHandleChat_UpstreamNon2xx_ProviderBlind(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil),
		fakeDispatch(http.StatusTooManyRequests, `{"error":{"message":"groq rate limit exceeded, retry after 2.5s"}}`, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 passthrough, got %d: %s", w.Code, w.Body.String())
	}
	assertNoLeak(t, w.Body.String())
}

func TestHandleChat_Unauthenticated(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	body, _ := json.Marshal(ChatRequest{Model: "hive-fast", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/chat", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleChat_MethodNotAllowed(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := httptest.NewRequest(http.MethodGet, "/v1/rag/chat", nil)
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleChat_InvalidBody(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := httptest.NewRequest(http.MethodPost, "/v1/rag/chat", strings.NewReader("{not json"))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestHandleChat_TopKDefaultAndCap(t *testing.T) {
	var audits []auditRecord
	h := newChatTestHandler(newFakeStore(), &fakeEmbedder{}, &audits,
		fakeSelectRoute("route-groq-fast", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	req := chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		TopK:     999999,
	}, uuid.New())
	w := httptest.NewRecorder()
	h.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with capped top_k, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuildContextBlock_Empty(t *testing.T) {
	if got := buildContextBlock(nil); !strings.Contains(got, "no relevant context") {
		t.Errorf("expected fallback text for empty citations, got %q", got)
	}
}

func TestLastUserMessage_ReturnsMostRecent(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "second"},
	}
	got, err := lastUserMessage(msgs)
	if err != nil || got != "second" {
		t.Errorf("expected %q, got %q (err=%v)", "second", got, err)
	}
}

func assertNoLeak(t *testing.T, body string) {
	t.Helper()
	for _, leak := range []string{"groq", "openrouter", "openai", "litellm", "ollama"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("response leaks provider name %q: %s", leak, body)
		}
	}
}
