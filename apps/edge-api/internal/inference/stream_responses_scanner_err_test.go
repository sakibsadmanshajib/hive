package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExecuteResponsesStreaming_OversizedUpstreamLine_ClientGetsHonestFailedEvent
// is the Responses API twin of the chat-completions regression guard
// (stream_scanner_err_test.go): same issue #1255 HIGH #1 defect
// (stream_responses.go never checked scanner.Err() either), same fix shape,
// this endpoint's own already-established convention (a typed
// response.failed lifecycle event mirroring response.completed) instead of
// the chat-completions inline error frame.
func TestExecuteResponsesStreaming_OversizedUpstreamLine_ClientGetsHonestFailedEvent(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := oversizedLineSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	orch.executeResponsesStreaming(context.Background(), w, req, []byte(`{}`), ResponsesRequest{Model: "gpt-4o"}, "gpt-4o",
		NeedFlags{NeedResponses: true, NeedStreaming: true}, 10000)

	body := w.Body.String()
	if !strings.Contains(body, "response.failed") {
		t.Fatalf("expected a client-visible response.failed event; body:\n%s", body)
	}
	if !strings.Contains(body, `"status":"failed"`) {
		t.Errorf("expected the response object's status to be failed; body:\n%s", body)
	}
	if strings.Contains(body, "response.completed") {
		t.Error("an aborted stream must not also claim response.completed")
	}

	if rec.has("/internal/accounting/reservations/release") {
		t.Error("content was delivered before the failure: must not release in full")
	}
	fbody, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation despite the aborted stream; calls seen: %+v", rec.calls)
	}
	// Captured and unconfirmed, at the catalog price of what was involved
	// rather than the flat hold (#1198). Nothing priceable survived the abort,
	// so the floor is the answer.
	assertPricedCapture(t, fbody, minimumCapture)
	if confirmed, _ := fbody["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must be false: no real usage arrived before the abort")
	}
}

// TestExecuteResponsesStreaming_NormalCompletion_BodyUnaffectedByScannerErrCheck
// is the Responses API twin of the chat-completions no-regression control:
// proves the new scanner.Err() branch does not fire for an ordinary
// completion, so response.completed still terminates the stream and
// response.failed never appears.
func TestExecuteResponsesStreaming_NormalCompletion_BodyUnaffectedByScannerErrCheck(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := completingSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	orch.executeResponsesStreaming(context.Background(), w, req, []byte(`{}`), ResponsesRequest{Model: "gpt-4o"}, "gpt-4o",
		NeedFlags{NeedResponses: true, NeedStreaming: true}, 10000)

	body := w.Body.String()
	if !strings.Contains(body, "response.completed") {
		t.Errorf("expected the stream to still end with response.completed; body:\n%s", body)
	}
	if strings.Contains(body, "response.failed") {
		t.Error("a normal completion must never emit the abort response.failed event")
	}
}

// TestExecuteResponsesStreaming_ClientDisconnect_NoSpuriousFailedEvent is the
// Responses API twin of stream_scanner_err_test.go's chat-completions
// MEDIUM-1 guard (go-review, PR #1271): r.Context() cancellation tears down
// the upstream body read the same way a real relay failure does, so
// scanner.Err() == context.Canceled on an ordinary client disconnect too.
// Without the ctx.Err() == nil guard in stream_responses.go, a routine
// cancellation would emit a spurious response.failed event to an
// already-dead socket, burying the real ErrTooLong signal this PR exists to
// surface.
func TestExecuteResponsesStreaming_ClientDisconnect_NoSpuriousFailedEvent(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	litellmSrv := gatedSSEServer("partial responses-api reply before disconnect", ready)
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		orch.executeResponsesStreaming(ctx, w, req, []byte(`{}`), ResponsesRequest{Model: "gpt-4o"}, "gpt-4o",
			NeedFlags{NeedResponses: true, NeedStreaming: true}, 10000)
	}()

	waitReady(t, ready)
	cancel()
	waitDone(t, done)

	body := w.Body.String()
	if strings.Contains(body, "response.failed") {
		t.Error("a client disconnect must never emit response.failed: that would bury real relay failures under routine cancellations")
	}
}
