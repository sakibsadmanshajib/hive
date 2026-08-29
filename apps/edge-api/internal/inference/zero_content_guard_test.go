package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// emptyLengthBody is a chat completion in the reasoning-burn shape: 200,
// finish_reason=length, content null (coerced to ""), usage billed in full.
func emptyLengthBody(promptTokens int) string {
	return fmt.Sprintf(`{"id":"chatcmpl-empty","object":"chat.completion","created":1,"model":"route","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"length"}],"usage":{"prompt_tokens":%d,"completion_tokens":512,"total_tokens":%d}}`,
		promptTokens, promptTokens+512)
}

// contentBody is an ordinary completion with visible content.
func contentBody() string {
	return `{"id":"chatcmpl-ok","object":"chat.completion","created":1,"model":"route","choices":[{"index":0,"message":{"role":"assistant","content":"hello there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`
}

// newRoutingMockReserving behaves like newRoutingMock but carries the given
// pool reasoning reserve, which is what arms the #1171 mechanisms.
func newRoutingMockReserving(litellmURL string, reserve int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:                "gpt-4o",
			RouteID:                "route-test-1",
			LiteLLMModelName:       "openrouter/openai/gpt-4o",
			Provider:               "openrouter",
			Pricing:                catalogHiveFast,
			PriceUnit:              PriceUnitTokens,
			ReasoningReserveTokens: reserve,
		})
	}))
}

// scriptedLiteLLM answers each dispatch with the next body from script,
// recording how many requests it saw and what bodies were sent to it.
type scriptedLiteLLM struct {
	server *httptest.Server
	hits   int64

	mu     sync.Mutex
	bodies []string
}

// sentBody returns the request body of the nth dispatch (0-based).
func (s *scriptedLiteLLM) sentBody(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[n]
}

func newScriptedLiteLLM(t *testing.T, script []string) *scriptedLiteLLM {
	s := &scriptedLiteLLM{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&s.hits, 1)
		buf, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(buf))
		s.mu.Unlock()
		idx := int(n) - 1
		if idx >= len(script) {
			idx = len(script) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, script[idx])
	}))
	return s
}

// TestExecuteSync_ZeroContentLength_RetriesThenSucceeds is the happy branch
// of the guard: first member answers empty-length, the retry lands on a
// different member and returns real content. The caller gets the retried
// response; settlement prices it normally.
func TestExecuteSync_ZeroContentLength_RetriesThenSucceeds(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{emptyLengthBody(76), contentBody()})
	defer litellm.server.Close()
	routing := newRoutingMockReserving(litellm.server.URL, 4096)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello"}],"max_tokens":200}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	if code := w.Code; code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", code, w.Body.String())
	}
	if got := atomic.LoadInt64(&litellm.hits); got != 2 {
		t.Fatalf("provider dispatched %d time(s), want exactly the original plus one retry", got)
	}
	if hdr := w.Header().Get(emptyContentHeader); hdr != "" {
		t.Errorf("%s header set on a successful retry; must be absent", emptyContentHeader)
	}
	if !strings.Contains(w.Body.String(), "hello there") {
		t.Errorf("caller did not receive the retried content: %s", w.Body.String())
	}

	// Settlement priced the retried response's own usage, not a hold capture.
	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	if confirmed, _ := finalize["terminal_usage_confirmed"].(bool); !confirmed {
		t.Errorf("terminal_usage_confirmed = false after a successful retry; want true")
	}
	if credits, _ := finalize["actual_credits"].(float64); credits >= float64(DefaultHoldText) {
		t.Errorf("actual_credits = %v, want the retried response's small usage price, not a capture at the hold (%d)", credits, DefaultHoldText)
	}
}

// TestExecuteSync_ZeroContentLength_Twice_CapturesHold pins the fail-closed
// branch: both members answer empty-length, so the request settles by
// capturing the reservation hold (terminal_usage_confirmed=false) instead of
// billing a full-price success for content that does not exist.
//
// The request deliberately sets NO completion ceiling, which is what leaves the
// capture at the full hold: a caller who does set one bounds the capture too
// (issue #1283), and TestExecuteSync_ZeroContentCaptureBoundedByCallerCeiling
// pins that half.
func TestExecuteSync_ZeroContentLength_Twice_CapturesHold(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{emptyLengthBody(76), emptyLengthBody(76)})
	defer litellm.server.Close()
	routing := newRoutingMockReserving(litellm.server.URL, 4096)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello"}]}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	if got := atomic.LoadInt64(&litellm.hits); got != 2 {
		t.Fatalf("provider dispatched %d time(s), want original plus exactly one retry", got)
	}
	if hdr := w.Header().Get(emptyContentHeader); hdr != "length" {
		t.Errorf("%s = %q, want %q so an SDK client is not left guessing", emptyContentHeader, hdr, "length")
	}

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	if confirmed, _ := finalize["terminal_usage_confirmed"].(bool); confirmed {
		t.Errorf("terminal_usage_confirmed = true on a captured hold; want false so reconciliation still sees it")
	}
	// The capture is at reservation.Held(): the hold the control-plane
	// actually confirmed in its CreateReservation answer (the accounting
	// mock's EstimatedCredits of 10,000), not the pre-dispatch floor.
	if credits, _ := finalize["actual_credits"].(float64); credits != 10000 {
		t.Errorf("actual_credits = %v, want a capture at the confirmed hold (10000)", credits)
	}
}

// TestExecuteSync_NonReservingAlias_NeverRetries pins scope: reserve 0 (every
// ordinary alias) keeps exactly one dispatch even on an empty-length answer.
// TestExecuteSync_ZeroContentLength_RetriesOnAZeroReserveRoute is the inverse
// of what this test used to assert. It required that a route with no reasoning
// reserve NEVER retried an empty length completion, on the reading that the
// reserve column marks the pools that reason. It does not: it marks the three
// free-pool members enumerated in migration 20260826_01, and
// deepseek-v4-flash carries the column default of zero while reasoning
// heavily. Measured on the demo box on 2026-08-29, second turn of a tool round
// trip at ceiling 120: two of eight attempts came back with content null,
// finish_reason length and reasoning_tokens of 117 and 124. Under the old gate
// those reached the caller as an empty string with no retry, which is what
// made the live conformance suite fail intermittently on the multi-turn tool
// path that Claude Code compatibility rests on.
func TestExecuteSync_ZeroContentLength_RetriesOnAZeroReserveRoute(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{emptyLengthBody(76), contentBody()})
	defer litellm.server.Close()
	routing := newRoutingMock(litellm.server.URL) // reserve 0
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello"}],"max_tokens":200}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	if got := atomic.LoadInt64(&litellm.hits); got != 2 {
		t.Errorf("provider dispatched %d time(s), want 2: an empty length completion is worth one retry whatever route produced it", got)
	}
	if hdr := w.Header().Get(emptyContentHeader); hdr != "" {
		t.Errorf("%s set even though the retry produced content; must stay absent", emptyContentHeader)
	}
	if body := w.Body.String(); !strings.Contains(body, "hello there") {
		t.Errorf("caller got the empty first answer rather than the retry: %s", body)
	}
}

func TestIsEmptyLengthCompletion(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"empty content length", emptyLengthBody(76), true},
		{"visible content stop", contentBody(), false},
		{"tool call with null content is spec-correct",
			`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"1","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, false},
		{"refusal counts as visible output",
			`{"choices":[{"message":{"role":"assistant","content":"","refusal":"no"},"finish_reason":"length"}]}`, false},
		{"stop finish with empty text is a genuine empty answer, not the burn shape",
			`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`, false},
		{"no choices", `{"choices":[]}`, false},
	}
	for _, tc := range cases {
		if got := isEmptyLengthCompletion([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: isEmptyLengthCompletion = %v, want %v (body: %s)", tc.name, got, tc.want, tc.body)
		}
	}
}
