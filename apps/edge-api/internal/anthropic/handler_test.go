package anthropic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// fakeChat stands in for the wired POST /v1/chat/completions handler chain that
// the Anthropic surface delegates to. It records the sub-request so the tests
// can assert what /v1/messages actually asked the OpenAI path to do.
type fakeChat struct {
	calls   int
	path    string
	method  string
	rawBody string
	body    map[string]any

	respond func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeChat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls++
	f.path = r.URL.Path
	f.method = r.Method
	raw, _ := io.ReadAll(r.Body)
	f.rawBody = string(raw)
	f.body = map[string]any{}
	_ = json.Unmarshal(raw, &f.body)
	if f.respond != nil {
		f.respond(w, r)
	}
}

// respondSSE replies with an OpenAI streaming body, the shape both the JWT
// dispatcher and the API-key orchestrator emit.
func respondSSE(chunks ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// respondJSON replies with a single OpenAI chat completion body, the shape the
// API-key orchestrator emits for a non-streaming request.
func respondJSON(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)
	}
}

func respondStatus(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

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

// flushRecorder counts Flush calls so a test can prove a streamed response is
// emitted incrementally rather than buffered until the handler returns.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	chat := &fakeChat{}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: want 405 got %d", rec.Code)
	}
	if chat.calls != 0 {
		t.Errorf("downstream calls: want 0 got %d", chat.calls)
	}
}

// An anonymous request carries no session principal. Authorization for that
// case belongs to the API-key path behind the delegated handler, so the request
// must reach it rather than being refused here.
func TestHandler_AnonymousRequestIsDelegatedForAPIKeyAuthorization(t *testing.T) {
	chat := &fakeChat{respond: respondStatus(http.StatusUnauthorized,
		`{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error","code":"invalid_api_key"}}`)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if chat.calls != 1 {
		t.Fatalf("downstream calls: want 1 got %d", chat.calls)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_api_key") {
		t.Errorf("downstream auth error not forwarded: %s", rec.Body.String())
	}
}

func TestHandler_NoTenantIsRefusedBeforeDelegation(t *testing.T) {
	chat := &fakeChat{}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), Role: "member"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: want 403 got %d", rec.Code)
	}
	if chat.calls != 0 {
		t.Errorf("downstream calls: want 0 got %d", chat.calls)
	}
}

func TestHandler_NoRoleIsRefusedBeforeDelegation(t *testing.T) {
	chat := &fakeChat{}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: uuid.New(), Role: "guest"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: want 403 got %d", rec.Code)
	}
	if chat.calls != 0 {
		t.Errorf("downstream calls: want 0 got %d", chat.calls)
	}
}

func TestHandler_RequestValidationHappensBeforeDelegation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"bad json", `{not valid json}`},
		{"missing model", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":5}`},
		{"missing messages", `{"model":"m","max_tokens":5}`},
		{"missing max_tokens", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`},
		{"zero max_tokens", `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chat := &fakeChat{}
			h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newAuthedRequest(t, tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status: want 400 got %d", rec.Code)
			}
			if chat.calls != 0 {
				t.Errorf("downstream calls: want 0 got %d", chat.calls)
			}
		})
	}
}

// The defect this test locks down: /v1/messages must hand the request to the
// OpenAI chat-completions path (which resolves the alias through SelectRoute and
// meters the call) instead of building its own upstream dispatch.
func TestHandler_DelegatesToChatCompletionsPathWithFullBody(t *testing.T) {
	chat := &fakeChat{respond: respondSSE(
		`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
	)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	body := `{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":64,` +
		`"temperature":0.2,"stop_sequences":["END"],` +
		`"tools":[{"name":"lookup","description":"look it up","input_schema":{"type":"object","properties":{}}}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t, body))

	if chat.calls != 1 {
		t.Fatalf("downstream calls: want 1 got %d", chat.calls)
	}
	if chat.path != "/v1/chat/completions" {
		t.Errorf("delegated path: want /v1/chat/completions got %q", chat.path)
	}
	if chat.method != http.MethodPost {
		t.Errorf("delegated method: want POST got %q", chat.method)
	}
	// The client alias is forwarded untouched: alias resolution is the routing
	// layer's job, and this surface must never name a route itself.
	if chat.body["model"] != "hive-fast" {
		t.Errorf("delegated model: want hive-fast got %v", chat.body["model"])
	}
	// Downstream must stream so per-chunk usage settles the reservation at real
	// token counts instead of the flat estimate.
	if chat.body["stream"] != true {
		t.Errorf("delegated stream: want true got %v", chat.body["stream"])
	}
	streamOpts, ok := chat.body["stream_options"].(map[string]any)
	if !ok || streamOpts["include_usage"] != true {
		t.Errorf("delegated stream_options: want include_usage true got %v", chat.body["stream_options"])
	}
	if chat.body["max_tokens"] != float64(64) {
		t.Errorf("delegated max_tokens: want 64 got %v", chat.body["max_tokens"])
	}
	if chat.body["temperature"] != float64(0.2) {
		t.Errorf("delegated temperature: want 0.2 got %v", chat.body["temperature"])
	}
	tools, ok := chat.body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("delegated tools: want 1 tool got %v", chat.body["tools"])
	}
	if !strings.Contains(chat.rawBody, `"stop":["END"]`) {
		t.Errorf("delegated stop sequences missing: %s", chat.rawBody)
	}
}

func TestHandler_NonStreamFoldsDownstreamStreamIntoOneMessage(t *testing.T) {
	chat := &fakeChat{respond: respondSSE(
		`{"id":"chatcmpl-fold","model":"route-groq-fast","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`{"id":"chatcmpl-fold","model":"route-groq-fast","choices":[{"index":0,"delta":{"content":"lo!"}}]}`,
		`{"id":"chatcmpl-fold","model":"route-groq-fast","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: want application/json got %q", ct)
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
	if got.Model != "hive-fast" {
		t.Errorf("model: want hive-fast got %q", got.Model)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "Hello!" {
		t.Errorf("content: %+v", got.Content)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 5 {
		t.Errorf("usage: %+v", got.Usage)
	}
	if strings.Contains(rec.Body.String(), "route-groq-fast") {
		t.Error("upstream route id leaked into the response")
	}
}

func TestHandler_NonStreamFoldsToolCallsFromDownstreamStream(t *testing.T) {
	chat := &fakeChat{respond: respondSSE(
		`{"id":"chatcmpl-tool","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`{"id":"chatcmpl-tool","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"bd\"}"}}]}}]}`,
		`{"id":"chatcmpl-tool","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
	)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got anthropic.MessagesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.StopReason != "tool_use" {
		t.Errorf("stop_reason: want tool_use got %q", got.StopReason)
	}
	if len(got.Content) != 1 {
		t.Fatalf("content blocks: want 1 got %d (%+v)", len(got.Content), got.Content)
	}
	block := got.Content[0]
	if block.Type != "tool_use" || block.ID != "call_1" || block.Name != "lookup" {
		t.Errorf("tool_use block: %+v", block)
	}
	if string(block.Input) != `{"q":"bd"}` {
		t.Errorf("tool input: want {\"q\":\"bd\"} got %s", block.Input)
	}
}

func TestHandler_NonStreamTranslatesDownstreamJSON(t *testing.T) {
	chat := &fakeChat{respond: respondJSON(
		`{"id":"chatcmpl-sync","object":"chat.completion","model":"hive-fast",` +
			`"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"Hello!"}}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got anthropic.MessagesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "Hello!" {
		t.Errorf("content: %+v", got.Content)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 5 {
		t.Errorf("usage: %+v", got.Usage)
	}
}

func TestHandler_StreamTranslatesDownstreamSSE(t *testing.T) {
	chat := &fakeChat{respond: respondSSE(
		`{"id":"chatcmpl-s","model":"route-groq-fast","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"id":"chatcmpl-s","model":"route-groq-fast","choices":[{"index":0,"delta":{"content":"Hi"}}]}`,
		`{"id":"chatcmpl-s","model":"route-groq-fast","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
	)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"my-alias","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"stream":true}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type: want text/event-stream got %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"message_start", "content_block_delta", "message_delta", "message_stop", "my-alias"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "route-groq-fast") {
		t.Error("upstream route id leaked in stream")
	}
	// Events must reach the client as they arrive, not in one buffered burst.
	if rec.flushes < 3 {
		t.Errorf("stream flushes: want at least 3 got %d", rec.flushes)
	}
}

// A downstream chain that keeps writing after the [DONE] sentinel must not
// produce extra Anthropic events. The translator is fed line by line here (the
// delegated path writes into a ResponseWriter, so nothing consumes FeedLine's
// return value), which is exactly the caller shape that would otherwise
// translate trailing frames.
func TestHandler_StreamIgnoresDownstreamFramesAfterDone(t *testing.T) {
	chat := &fakeChat{respond: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range []string{
			`data: {"id":"chatcmpl-s","choices":[{"index":0,"delta":{"content":"Hi"}}]}`,
			`data: [DONE]`,
			`data: {"id":"chatcmpl-s","choices":[{"index":0,"delta":{"content":"TRAILING"}}]}`,
		} {
			fmt.Fprintf(w, "%s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"my-alias","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"stream":true}`))

	body := rec.Body.String()
	if !strings.Contains(body, "Hi") {
		t.Errorf("content before [DONE] missing: %s", body)
	}
	if strings.Contains(body, "TRAILING") {
		t.Errorf("frame after [DONE] was translated: %s", body)
	}
	if got := strings.Count(body, "event: message_stop"); got != 1 {
		t.Errorf("message_stop count: want 1 got %d: %s", got, body)
	}
}

// Errors raised by the delegated chain are already provider-blind (that path
// owns the upstream boundary), so they are forwarded unchanged rather than
// re-wrapped, status included.
func TestHandler_DownstreamErrorIsForwardedWithStatus(t *testing.T) {
	chat := &fakeChat{respond: respondStatus(http.StatusForbidden,
		`{"error":{"message":"model not available for this workspace","type":"forbidden"}}`)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-premium","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403 got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model not available for this workspace") {
		t.Errorf("downstream refusal not forwarded: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "event: message_start") {
		t.Error("error response must not be emitted as a stream")
	}
}

func TestHandler_DownstreamErrorOnStreamingRequestIsNotStreamed(t *testing.T) {
	chat := &fakeChat{respond: respondStatus(http.StatusTooManyRequests,
		`{"error":{"message":"hive-fast is temporarily rate limited.","type":"rate_limit_error","code":"upstream_rate_limited"}}`)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"stream":true}`))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: want 429 got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "message_start") {
		t.Errorf("error must not be translated into stream events: %s", rec.Body.String())
	}
}

func TestHandler_MissingDownstreamHandlerFailsClosed(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500 got %d", rec.Code)
	}
}

func TestHandler_EmptyDownstreamResponseIsAnUpstreamError(t *testing.T) {
	chat := &fakeChat{respond: func(w http.ResponseWriter, r *http.Request) {}}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: want 502 got %d body=%s", rec.Code, rec.Body.String())
	}
}

// T1: count_tokens is a local estimate with no dispatch, and keeps its own
// session guards.
func TestHandler_CountTokens_RequiresAuth(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: &fakeChat{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no user: want 401 got %d", rec.Code)
	}
}

func TestHandler_CountTokens_RequiresTenant(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: &fakeChat{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), Role: "member"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no tenant: want 403 got %d", rec.Code)
	}
}

func TestHandler_CountTokens_RequiresRole(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: &fakeChat{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: uuid.New(), Role: "guest"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no role: want 403 got %d", rec.Code)
	}
}

func TestHandler_CountTokens(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: &fakeChat{}})
	req := newAuthedRequest(t, `{"model":"m","messages":[{"role":"user","content":"Hello world"}],"max_tokens":5}`)
	req.URL.Path = "/v1/messages/count_tokens"
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

// anthropicErrorEnvelope mirrors the wire shape a real Anthropic SDK parses
// (top-level "type":"error", nested error.type/error.message), decoded here
// rather than via an exported Hive type: what matters is the JSON a client
// actually receives, not an internal Go representation of it.
type anthropicErrorEnvelope struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// TestHandler_ValidationErrorUsesAnthropicEnvelope is the compliance guard for
// this surface's own refusals (never delegated): before this, every one used
// the OpenAI envelope ({"error":{"message","type"}}, no top-level "type"),
// which the real Anthropic SDK's exception .type attribute reads as
// body["error"]["type"] and never finds without the wrapper.
func TestHandler_ValidationErrorUsesAnthropicEnvelope(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: &fakeChat{}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)) // missing max_tokens

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400 got %d", rec.Code)
	}
	var got anthropicErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.Type != "error" {
		t.Errorf(`top-level type: want "error" got %q`, got.Type)
	}
	if got.Error.Type != "invalid_request_error" {
		t.Errorf("error.type: want invalid_request_error got %q", got.Error.Type)
	}
	if got.Error.Message == "" {
		t.Error("error.message: want non-empty")
	}
}

// TestHandler_DownstreamErrorUsesAnthropicEnvelope is the same guard for a
// delegated refusal, reshaped rather than forwarded verbatim in the OpenAI
// envelope it arrived in.
func TestHandler_DownstreamErrorUsesAnthropicEnvelope(t *testing.T) {
	chat := &fakeChat{respond: respondStatus(http.StatusForbidden,
		`{"error":{"message":"model not available for this workspace","type":"forbidden"}}`)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-premium","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403 got %d", rec.Code)
	}
	var got anthropicErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.Type != "error" {
		t.Errorf(`top-level type: want "error" got %q`, got.Type)
	}
	// Status drives the mapped type, not whatever the delegated chain's own
	// OpenAI-shaped "type" string said ("forbidden" is not a member of
	// Anthropic's error-type enum; "permission_error" is the 403 mapping).
	if got.Error.Type != "permission_error" {
		t.Errorf("error.type: want permission_error got %q", got.Error.Type)
	}
	if got.Error.Message != "model not available for this workspace" {
		t.Errorf("error.message: want the delegated refusal text got %q", got.Error.Message)
	}
}

// TestHandler_UpstreamErrorUsesAnthropicEnvelope pins the same shape on the
// writeUpstreamError path (an empty or oversized delegated response).
func TestHandler_UpstreamErrorUsesAnthropicEnvelope(t *testing.T) {
	chat := &fakeChat{respond: func(w http.ResponseWriter, r *http.Request) {}}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502 got %d", rec.Code)
	}
	var got anthropicErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.Type != "error" || got.Error.Type != "api_error" {
		t.Errorf("envelope: want type=error error.type=api_error got type=%q error.type=%q", got.Type, got.Error.Type)
	}
}

func TestHandler_CountTokens_BadJSON(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: &fakeChat{}})
	req := newAuthedRequest(t, `{bad}`)
	req.URL.Path = "/v1/messages/count_tokens"
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

// newCountTokensRequest builds an unauthenticated (no session user)
// count_tokens request carrying an API-key credential, which is how every real
// Anthropic SDK integration reaches this route.
func newCountTokensRequest(authHeader string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hello world"}],"max_tokens":5}`))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

// TestHandler_CountTokens_AcceptsAPIKeyPrincipal is the issue #1261 guard.
// count_tokens is the only route on this surface that does not delegate to the
// chat chain, so it used to recognize a JWT session principal and nothing
// else. An "hk_" request is routed past the JWT middleware by auth.Selector and
// therefore carries no session user at all, which made every programmatic
// caller a 401 on this one route while the sibling /v1/messages accepted the
// identical key on the identical connection.
func TestHandler_CountTokens_AcceptsAPIKeyPrincipal(t *testing.T) {
	var sawHeader string
	calls := 0
	h := anthropic.NewHandler(anthropic.Deps{
		OpenAIChat: &fakeChat{},
		AuthorizeAPIKey: func(_ context.Context, authHeader string) (*apierr.OpenAIError, map[string]string) {
			calls++
			sawHeader = authHeader
			return nil, nil
		},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCountTokensRequest("Bearer hk_live_test"))

	if rec.Code != http.StatusOK {
		t.Fatalf("API-key count_tokens: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("AuthorizeAPIKey called %d times, want exactly 1", calls)
	}
	if sawHeader != "Bearer hk_live_test" {
		t.Errorf("authorizer saw header %q, want the request's own Authorization value", sawHeader)
	}
	var got anthropic.CountTokensResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.InputTokens <= 0 {
		t.Errorf("input_tokens: want a positive estimate got %d", got.InputTokens)
	}
}

// TestHandler_CountTokens_RejectedAPIKeyKeepsTheAuthorizersOwnRefusal proves
// the refusal a caller sees is the authorizer's verdict reshaped into the
// Anthropic envelope, not the old blanket "missing user" string. The status
// alone cannot prove that (both are 401), so the message is what this asserts.
func TestHandler_CountTokens_RejectedAPIKeyKeepsTheAuthorizersOwnRefusal(t *testing.T) {
	code := "invalid_api_key"
	h := anthropic.NewHandler(anthropic.Deps{
		OpenAIChat: &fakeChat{},
		AuthorizeAPIKey: func(_ context.Context, _ string) (*apierr.OpenAIError, map[string]string) {
			refusal := apierr.NewError("invalid_request_error", "Invalid API key.", &code)
			return &refusal, nil
		},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCountTokensRequest("Bearer hk_revoked"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key: want 401 got %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["type"] != "error" {
		t.Errorf("envelope: want top-level type=error got %v", body["type"])
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != "authentication_error" {
		t.Errorf("error.type: want authentication_error got %v", errObj["type"])
	}
	if errObj["message"] != "Invalid API key." {
		t.Errorf("error.message: want the authorizer's own refusal got %v", errObj["message"])
	}
	if errObj["code"] != "invalid_api_key" {
		t.Errorf("error.code: want invalid_api_key got %v", errObj["code"])
	}
}

// TestHandler_CountTokens_WithoutAnAPIKeyAuthorityFailsClosed pins the
// deliberate degradation: a Handler wired without AuthorizeAPIKey stays
// session-only and refuses, rather than admitting an unauthenticated caller.
func TestHandler_CountTokens_WithoutAnAPIKeyAuthorityFailsClosed(t *testing.T) {
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: &fakeChat{}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCountTokensRequest("Bearer hk_live_test"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no API-key authority wired: want 401 got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandler_CountTokens_SessionPrincipalDoesNotConsultTheAPIKeyAuthority
// keeps the two principals independent: a signed-in user must still be served
// from the session checks alone, so a deployment whose authorizer is degraded
// does not start refusing browser traffic.
func TestHandler_CountTokens_SessionPrincipalDoesNotConsultTheAPIKeyAuthority(t *testing.T) {
	calls := 0
	h := anthropic.NewHandler(anthropic.Deps{
		OpenAIChat: &fakeChat{},
		AuthorizeAPIKey: func(_ context.Context, _ string) (*apierr.OpenAIError, map[string]string) {
			calls++
			refusal := apierr.NewError("invalid_request_error", "Invalid API key.", nil)
			return &refusal, nil
		},
	})
	req := newAuthedRequest(t, `{"model":"m","messages":[{"role":"user","content":"hello world"}],"max_tokens":5}`)
	req.URL.Path = "/v1/messages/count_tokens"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("session count_tokens: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Errorf("API-key authority consulted %d times for a session principal, want 0", calls)
	}
}
