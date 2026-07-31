package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// --- fail-closed reservation harness ---
//
// A request must never reach a provider unless a credit reservation was
// actually created, except for an enumerated set of infrastructure-transient
// control-plane faults. These tests drive the real executeSync,
// executeStreaming and executeResponsesStreaming lifecycles against a
// control-plane stand-in that answers CreateReservation with a chosen status,
// and count how many times the provider stand-in was reached.

// newAccountingMockReservationStatus answers CreateReservation with the given
// status code and raw body, and 200 for every other accounting/usage path, so
// a test can pick exactly which reservation outcome the edge-api sees.
func newAccountingMockReservationStatus(rec *accountingRecorder, status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		rec.record(r.URL.Path, decoded)
		if r.URL.Path == "/internal/accounting/reservations" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/internal/usage/attempts" {
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "accepted",
			})
		}
	}))
}

// countingJSONServer stands in for the provider (via LiteLLM) on the
// non-streaming path and counts how many times it was dispatched to.
func countingJSONServer(hits *int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:     "chatcmpl-test-1",
			Object: "chat.completion",
			Model:  "route",
			Usage:  &UsageResponse{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
		})
	}))
}

// countingSSEServer stands in for the provider on the streaming paths and
// counts how many times it was dispatched to.
func countingSSEServer(hits *int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "hello", nil))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// deadAccountingURL returns the URL of an already-closed server, so calls to
// it fail with a real connection-refused transport error rather than a
// simulated one.
func deadAccountingURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

func callSync(orch *Orchestrator) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, 10000, orch.litellm.ChatCompletion, normalizeChatCompletion)
	return w
}

func callStreaming(orch *Orchestrator) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)
	return w
}

func callResponsesStreaming(orch *Orchestrator) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	orch.executeResponsesStreaming(context.Background(), w, req, []byte(`{}`),
		ResponsesRequest{Model: "gpt-4o"}, "gpt-4o",
		NeedFlags{NeedResponses: true, NeedStreaming: true}, 10000)
	return w
}

// assertRefused checks the customer-facing refusal: the expected HTTP status,
// no provider dispatch at all, no provider identity in the payload, and no
// currency, FX or USD language of any kind (Bangladesh regulatory rule).
func assertRefused(t *testing.T, w *httptest.ResponseRecorder, hits int64, wantStatus int) apierrors.OpenAIErrorBody {
	t.Helper()
	if hits != 0 {
		t.Errorf("provider was dispatched %d time(s); a refused reservation must never reach a provider", hits)
	}
	if w.Code != wantStatus {
		t.Errorf("status = %d, want %d (body: %s)", w.Code, wantStatus, w.Body.String())
	}
	var payload apierrors.OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("refusal body is not an OpenAI error envelope: %v (body: %s)", err, w.Body.String())
	}
	lower := strings.ToLower(w.Body.String())
	for _, banned := range []string{"openrouter", "groq", "litellm", "$", "usd", "bdt", "taka", "exchange rate", "fx"} {
		if strings.Contains(lower, banned) {
			t.Errorf("refusal payload leaks %q: %s", banned, w.Body.String())
		}
	}
	return payload.Error
}

// TestExecuteSync_ReservationRejected_RefusesWithoutDispatch is the primary
// regression guard: control-plane answers 409 (credit policy rejected the
// hold, so no credit_reservations row exists), and the request must be
// refused instead of served for free.
func TestExecuteSync_ReservationRejected_RefusesWithoutDispatch(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	acctSrv := newAccountingMockReservationStatus(&accountingRecorder{}, http.StatusConflict,
		`{"error":"accounting: reservation exceeds available credits"}`)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callSync(orch)

	oerr := assertRefused(t, w, atomic.LoadInt64(&hits), http.StatusTooManyRequests)
	if oerr.Code == nil || *oerr.Code != "insufficient_quota" {
		t.Errorf("error.code = %v, want insufficient_quota", oerr.Code)
	}
}

// TestExecuteSync_ReservationRateLimited_RefusesWithoutDispatch covers a 429
// from the reservation call: also a refusal, never a free request.
func TestExecuteSync_ReservationRateLimited_RefusesWithoutDispatch(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	acctSrv := newAccountingMockReservationStatus(&accountingRecorder{}, http.StatusTooManyRequests,
		`{"error":"too many reservation attempts"}`)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callSync(orch)

	assertRefused(t, w, atomic.LoadInt64(&hits), http.StatusTooManyRequests)
}

// TestExecuteSync_ReservationUnrecognizedError_RefusesWithoutDispatch is the
// regression guard for the whole bug class: an error nobody enumerated (here a
// 400 validation rejection) must fail closed, not fall through to the provider.
func TestExecuteSync_ReservationUnrecognizedError_RefusesWithoutDispatch(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	acctSrv := newAccountingMockReservationStatus(&accountingRecorder{}, http.StatusBadRequest,
		`{"error":"estimated_credits must be positive"}`)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callSync(orch)

	assertRefused(t, w, atomic.LoadInt64(&hits), http.StatusServiceUnavailable)
}

// TestExecuteSync_ReservationControlPlane5xx_ProceedsUnreserved pins the
// transient side of the classification: a control-plane 5xx is an
// infrastructure fault, so paying traffic still flows rather than hard-failing
// on a control-plane blip.
func TestExecuteSync_ReservationControlPlane5xx_ProceedsUnreserved(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	acctSrv := newAccountingMockReservationStatus(&accountingRecorder{}, http.StatusInternalServerError,
		`{"error":"accounting: get balance: connection reset"}`)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callSync(orch)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: a transient control-plane fault must not hard-fail the request (body: %s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("provider dispatched %d time(s), want 1 on the transient path", got)
	}
}

// TestExecuteSync_ReservationUnreachable_ProceedsUnreserved covers the other
// transient condition: the control-plane is not answering at all
// (connection refused), which must behave like the 5xx case.
func TestExecuteSync_ReservationUnreachable_ProceedsUnreserved(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(deadAccountingURL(t), routingSrv.URL, litellmSrv.URL)
	w := callSync(orch)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: an unreachable control-plane must not hard-fail the request (body: %s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("provider dispatched %d time(s), want 1 on the transient path", got)
	}
}

// TestExecuteStreaming_ReservationRejected_RefusesWithoutDispatch asserts the
// streaming path refuses identically to the non-streaming one; both were
// broken by the same string-matching classification.
func TestExecuteStreaming_ReservationRejected_RefusesWithoutDispatch(t *testing.T) {
	var hits int64
	litellmSrv := countingSSEServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	acctSrv := newAccountingMockReservationStatus(&accountingRecorder{}, http.StatusConflict,
		`{"error":"accounting: reservation exceeds available credits"}`)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callStreaming(orch)

	oerr := assertRefused(t, w, atomic.LoadInt64(&hits), http.StatusTooManyRequests)
	if oerr.Code == nil || *oerr.Code != "insufficient_quota" {
		t.Errorf("error.code = %v, want insufficient_quota", oerr.Code)
	}
}

// TestExecuteStreaming_ReservationUnrecognizedError_RefusesWithoutDispatch is
// the streaming half of the bug-class guard.
func TestExecuteStreaming_ReservationUnrecognizedError_RefusesWithoutDispatch(t *testing.T) {
	var hits int64
	litellmSrv := countingSSEServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	acctSrv := newAccountingMockReservationStatus(&accountingRecorder{}, http.StatusBadRequest,
		`{"error":"estimated_credits must be positive"}`)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callStreaming(orch)

	assertRefused(t, w, atomic.LoadInt64(&hits), http.StatusServiceUnavailable)
}

// TestExecuteResponsesStreaming_ReservationRejected_RefusesWithoutDispatch
// covers the third dispatch site, the Responses API streaming lifecycle.
func TestExecuteResponsesStreaming_ReservationRejected_RefusesWithoutDispatch(t *testing.T) {
	var hits int64
	litellmSrv := countingSSEServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	acctSrv := newAccountingMockReservationStatus(&accountingRecorder{}, http.StatusConflict,
		`{"error":"accounting: reservation exceeds available credits"}`)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callResponsesStreaming(orch)

	oerr := assertRefused(t, w, atomic.LoadInt64(&hits), http.StatusTooManyRequests)
	if oerr.Code == nil || *oerr.Code != "insufficient_quota" {
		t.Errorf("error.code = %v, want insufficient_quota", oerr.Code)
	}
}
