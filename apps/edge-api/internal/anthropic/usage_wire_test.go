package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// This file wires the REAL /v1/chat/completions chain behind /v1/messages, the
// same delegation main.go performs, and asserts on the SERIALIZED response the
// Anthropic client receives. The hand-built OAI fixtures elsewhere in this
// package cannot catch issue #1329 at all: they hand the translator a stream
// that already carries usage, while the defect was the relay in front of it
// dropping the only usage frame LiteLLM sends. A struct-level or fixture-level
// assertion passes either way; only the bytes tell the truth.
//
// The upstream frames are the shape LiteLLM v1.98.0 relays, captured against
// the free pool own upstream on 2026-08-28: one content frame, one
// finish_reason frame, then the terminal usage frame carrying ONE empty-delta
// choice rather than an empty choices array.
const (
	liteContentFrame = `{"id":"gen-abc","object":"chat.completion.chunk","created":1787967930,"model":"upstream-route","choices":[{"index":0,"delta":{"role":"assistant","content":"quietwalk ok"}}]}`
	liteFinishFrame  = `{"id":"gen-abc","object":"chat.completion.chunk","created":1787967930,"model":"upstream-route","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	liteUsageFrame   = `{"id":"gen-abc","created":1787967930,"model":"upstream-route","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],"usage":{"completion_tokens":74,"prompt_tokens":78,"total_tokens":152,"completion_tokens_details":{"reasoning_tokens":62},"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"cost":0.0}}`
)

// newLiteLLMStub streams the captured LiteLLM frame sequence.
func newLiteLLMStub() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, frame := range []string{liteContentFrame, liteFinishFrame, liteUsageFrame} {
			fmt.Fprintf(w, "data: %s\n\n", frame)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// newRoutingStub answers route selection with a fixed-price token route, the
// shape control-plane always returns (#617, #627).
func newRoutingStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          "hive-free",
			RouteID:          "route-test-1",
			LiteLLMModelName: "openrouter/upstream-route",
			Provider:         "openrouter",
			Pricing:          inference.FixedPricing(10_500, 42_000),
			PriceUnit:        inference.PriceUnitTokens,
		}); err != nil {
			t.Errorf("routing stub encode: %v", err)
		}
	}))
}

// newAccountingStub answers reservation and attempt creation, so settlement
// runs its real code path without a database.
func newAccountingStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		var err error
		switch r.URL.Path {
		case "/internal/accounting/reservations":
			err = json.NewEncoder(w).Encode(inference.ReservationResult{
				ID: "res-test-1", AccountID: "acct-test-1", Status: "active", EstimatedCredits: 10000,
			})
		case "/internal/usage/attempts":
			err = json.NewEncoder(w).Encode(inference.AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "streaming",
			})
		}
		if err != nil {
			t.Errorf("accounting stub encode: %v", err)
		}
	}))
}

// messagesWire runs one POST /v1/messages through the real delegation chain and
// returns the bytes the Anthropic client received.
func messagesWire(t *testing.T, requestBody string) string {
	t.Helper()

	litellmSrv := newLiteLLMStub()
	defer litellmSrv.Close()
	routingSrv := newRoutingStub(t)
	defer routingSrv.Close()
	acctSrv := newAccountingStub(t)
	defer acctSrv.Close()

	client := &authz.Client{
		ResolveOverride: func(_ context.Context, _ string) (authz.AuthSnapshot, error) {
			return authz.AuthSnapshot{
				KeyID:          "key-test-1",
				AccountID:      "acct-test-1",
				TenantID:       "11111111-1111-1111-1111-111111111111",
				Status:         "active",
				AllowAllModels: true,
				BudgetKind:     "none",
			}, nil
		},
	}
	orch := inference.NewOrchestrator(
		authz.NewAuthorizer(client, nil),
		inference.NewRoutingClient(routingSrv.URL),
		inference.NewAccountingClient(acctSrv.URL),
		inference.NewLiteLLMClient(litellmSrv.URL, "test-key"),
	)

	handler := NewHandler(Deps{OpenAIChat: inference.NewHandler(orch)})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk_test-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body was:\n%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

// TestMessagesWire_SyncResponseCarriesRealUsage is the regression guard for
// issue #1329 on the non-streaming Anthropic response: the usage the pipeline
// already measured must reach the client. Asserted twice on purpose, once on
// the decoded numbers and once on the raw bytes: a struct assertion alone
// cannot tell a real count from a zero that omitempty or a nil slice smuggled
// through, which is the exact class of green signal this repo has shipped
// before.
func TestMessagesWire_SyncResponseCarriesRealUsage(t *testing.T) {
	wire := messagesWire(t, `{"model":"hive-free","max_tokens":128,"messages":[{"role":"user","content":"say quietwalk ok"}]}`)

	var got MessagesResponse
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v; body was:\n%s", err, wire)
	}
	if got.Usage.InputTokens != 78 {
		t.Errorf("usage.input_tokens = %d, want 78; body was:\n%s", got.Usage.InputTokens, wire)
	}
	if got.Usage.OutputTokens != 74 {
		t.Errorf("usage.output_tokens = %d, want 74; body was:\n%s", got.Usage.OutputTokens, wire)
	}
	if !strings.Contains(wire, `"input_tokens":78`) {
		t.Errorf("serialized usage must carry input_tokens 78; body was:\n%s", wire)
	}
	if !strings.Contains(wire, `"output_tokens":74`) {
		t.Errorf("serialized usage must carry output_tokens 74; body was:\n%s", wire)
	}
	if strings.Contains(wire, `"input_tokens":0`) || strings.Contains(wire, `"output_tokens":0`) {
		t.Errorf("zeroed usage reached the client, the whole defect in #1329; body was:\n%s", wire)
	}
}

// TestMessagesWire_StreamingMessageDeltaCarriesRealUsage covers the shape Claude
// Code actually consumes: a streamed response, whose authoritative token counts
// ride on message_delta. message_start is asserted for presence and shape only
// -- this gateway relays rather than serves the generation, so it cannot know
// the input count before the upstream reports it in a terminal frame, which is
// the pre-existing limitation documented on emitMessageStart.
func TestMessagesWire_StreamingMessageDeltaCarriesRealUsage(t *testing.T) {
	wire := messagesWire(t, `{"model":"hive-free","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"say quietwalk ok"}]}`)

	var deltaUsage *StreamUsage
	for _, line := range strings.Split(wire, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev StreamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("streamed event is not valid JSON: %s", line)
		}
		if ev.Type == "message_delta" {
			deltaUsage = ev.Usage
		}
	}

	if deltaUsage == nil {
		t.Fatalf("no message_delta usage reached the client; stream was:\n%s", wire)
	}
	if deltaUsage.InputTokens != 78 {
		t.Errorf("message_delta usage.input_tokens = %d, want 78; stream was:\n%s", deltaUsage.InputTokens, wire)
	}
	if deltaUsage.OutputTokens != 74 {
		t.Errorf("message_delta usage.output_tokens = %d, want 74; stream was:\n%s", deltaUsage.OutputTokens, wire)
	}
	if !strings.Contains(wire, `"input_tokens":78`) || !strings.Contains(wire, `"output_tokens":74`) {
		t.Errorf("serialized stream must carry the real counts; stream was:\n%s", wire)
	}
	if !strings.Contains(wire, `"type":"message_start"`) {
		t.Errorf("stream must open with message_start; stream was:\n%s", wire)
	}
}
