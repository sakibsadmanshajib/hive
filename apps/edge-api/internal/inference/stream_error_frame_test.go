package inference

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- issue #928 defect 4: a mid-stream provider error frame was swallowed ---
//
// data: {"error": {...}} unmarshals cleanly into ChatCompletionChunk with every
// field absent, so the relay rewrote its model, minted it an id and re-marshalled
// it into an empty chunk -- or dropped it outright when the caller had not asked
// for usage. The error payload never reached the client either way. Only an
// UNPARSEABLE frame ever reached the sanitizing fallback, and an error frame
// parses fine.
//
// Once the relay has committed 200 OK and the SSE headers there is no status
// left to fail with, so the honest ending is a gateway-owned error frame plus a
// log line. The upstream's own text is never it: the leak measured on
// /v1/rag/chat (PR #1303) carried the provider brand, a top-up URL and the
// customer's own quota state.

// upstreamErrorText is the shape a real provider failure takes on the wire,
// with every leak class the provider-blind rule exists for: a provider name, an
// account identifier, and a link to somebody else's billing page.
const upstreamErrorText = `openrouter: you exceeded your current quota for org-12345, top up at https://openrouter.ai/settings/credits`

// errorFrameSSEServer streams prelude (if non-empty) and then a provider error
// frame, with no [DONE] after it -- which is what a real upstream does, because
// the error IS the end of its stream.
func errorFrameSSEServer(prelude string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		if prelude != "" {
			fmt.Fprintln(w, prelude)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"insufficient_quota\",\"code\":429}}\n", upstreamErrorText)
		flusher.Flush()
	}))
}

// assertNoUpstreamLeak fails if any part of the upstream error text reached the
// caller. Checked token by token rather than as one string, because a partial
// forward (a scrub that kept the message but dropped the type, say) is the
// failure mode a whole-string check would miss.
func assertNoUpstreamLeak(t *testing.T, body string) {
	t.Helper()
	for _, leak := range []string{"openrouter", "org-12345", "settings/credits", "quota", "insufficient_quota"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("client body carries upstream detail %q: an error frame is REPLACED, never forwarded; body:\n%s", leak, body)
		}
	}
}

// TestExecuteStreaming_UpstreamErrorFrame_ReachesTheClientProviderBlind is the
// regression guard for issue #928 defect 4 on the chat relay: the frame is
// replaced by a gateway-owned one the client can render, rather than silently
// becoming an empty chunk or vanishing.
func TestExecuteStreaming_UpstreamErrorFrame_ReachesTheClientProviderBlind(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := errorFrameSSEServer("")
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, mockReservationHold, false, nil,
		orch.litellm.ChatCompletionStream)

	body := w.Body.String()
	if !strings.Contains(body, `"upstream_error"`) {
		t.Fatalf("expected a client-visible upstream_error frame; body:\n%s", body)
	}
	assertNoUpstreamLeak(t, body)
	if strings.Contains(body, "data: [DONE]") {
		t.Error("a stream that ended on an upstream error must not also claim a normal completion")
	}

	// Nothing was delivered, so the hold goes back whole. The error frame this
	// relay wrote is Hive's own and is not delivered work: counting it as one
	// would charge the customer for an upstream failure.
	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("nothing was delivered before the upstream error: the hold must be released, not charged")
	}
	rbody, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected ReleaseReservation for an error-only stream; calls seen: %+v", rec.calls)
	}
	if reason, _ := rbody["reason"].(string); reason != "upstream_error" {
		t.Errorf("release reason = %q, want %q", reason, "upstream_error")
	}
}

// TestExecuteStreaming_UpstreamErrorFrameAfterContent_StillBillsWhatWasDelivered
// is the other half of the money rule. The error frame ends the stream, but the
// tokens that reached the customer before it are delivered work and are charged
// for (D-034). A fix that released on every error frame would serve a
// nearly-complete answer for free.
func TestExecuteStreaming_UpstreamErrorFrameAfterContent_StillBillsWhatWasDelivered(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := errorFrameSSEServer(buildChunkLine("chunk-1", "route",
		"Here is the first half of a real answer that the customer has already received.", nil))
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, mockReservationHold, false, nil,
		orch.litellm.ChatCompletionStream)

	body := w.Body.String()
	if !strings.Contains(body, "first half of a real answer") {
		t.Errorf("the content delivered before the error must still reach the client; body:\n%s", body)
	}
	if !strings.Contains(body, `"upstream_error"`) {
		t.Errorf("expected the error frame after the content; body:\n%s", body)
	}
	assertNoUpstreamLeak(t, body)

	if rec.has("/internal/accounting/reservations/release") {
		t.Error("content was delivered before the error: releasing the hold in full would serve it free (D-034)")
	}
	fbody, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation for the delivered half; calls seen: %+v", rec.calls)
	}
	assertPricedCapture(t, fbody, mockReservationHold)
}

// TestExecuteResponsesStreaming_UpstreamErrorFrame_FailsAndIsNotBilled is the
// Responses API twin, where the same defect was strictly worse. That relay sets
// HasForwardedChunk on any frame it parses, and an error frame parses, so an
// error-only Responses stream was counted as delivered work and CHARGED while
// the caller received no lifecycle event at all.
func TestExecuteResponsesStreaming_UpstreamErrorFrame_FailsAndIsNotBilled(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := errorFrameSSEServer("")
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	orch.executeResponsesStreaming(context.Background(), w, req, []byte(`{}`), ResponsesRequest{Model: "gpt-4o"}, "gpt-4o",
		NeedFlags{NeedResponses: true, NeedStreaming: true}, mockReservationHold)

	body := w.Body.String()
	if !strings.Contains(body, "response.failed") {
		t.Fatalf("expected a client-visible response.failed event; body:\n%s", body)
	}
	if strings.Contains(body, "response.completed") {
		t.Error("a stream that ended on an upstream error must not also claim response.completed")
	}
	assertNoUpstreamLeak(t, body)

	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("an error-only Responses stream delivered nothing: charging it bills the customer for an upstream failure (#928 defect 4)")
	}
	if !rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("expected ReleaseReservation for an error-only Responses stream; calls seen: %+v", rec.calls)
	}
}

// TestExecuteStreaming_ErrorFrameCarryingContent_DeliversTheContentToo closes
// the gap the first version of this fix opened. The error check ran BEFORE the
// typed decode, so a frame carrying a content delta AND a top-level error had
// its content dropped: work the customer never saw and settlement never priced
// (review finding on PR #1762). The frame is now classified first and acted on
// last, so the content is relayed and accumulated before the stream ends.
func TestExecuteStreaming_ErrorFrameCarryingContent_DeliversTheContentToo(t *testing.T) {
	const delivered = "the model had already written this much when the provider gave up"

	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"route\","+
			"\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}],\"error\":{\"message\":%q,\"type\":\"insufficient_quota\"}}\n",
			delivered, upstreamErrorText)
		flusher.Flush()
	}))
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, mockReservationHold, false, nil,
		orch.litellm.ChatCompletionStream)

	body := w.Body.String()
	if !strings.Contains(body, delivered) {
		t.Errorf("the content the error frame also carried never reached the client; body:\n%s", body)
	}
	if !strings.Contains(body, `"upstream_error"`) {
		t.Errorf("expected the error frame after the content it carried; body:\n%s", body)
	}
	assertNoUpstreamLeak(t, body)

	// Delivered, so it bills: the content is real output the customer received.
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("content reached the customer: releasing the hold in full would serve it free (D-034)")
	}
	if !rec.has("/internal/accounting/reservations/finalize") {
		t.Fatalf("expected FinalizeReservation for the delivered content; calls seen: %+v", rec.calls)
	}
}
