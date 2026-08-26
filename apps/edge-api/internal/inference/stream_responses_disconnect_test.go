package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExecuteResponsesStreaming_ClientDisconnect_SettlesDeliveredTokensDespiteCancelledContext
// mirrors the chat-completions regression guard for the Responses API
// streaming path (stream_responses.go), which the ruling requires to receive
// an identical fix: one settle site, on a context that survives the client
// disconnecting.
func TestExecuteResponsesStreaming_ClientDisconnect_SettlesDeliveredTokensDespiteCancelledContext(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	litellmSrv := gatedSSEServer("partial responses-api reply", ready)
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

	if rec.has("/internal/accounting/reservations/release") {
		t.Error("nothing should be released in full: content was delivered")
	}

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation to reach control-plane despite the cancelled context; calls seen: %+v", rec.calls)
	}
	actual, _ := body["actual_credits"].(float64)
	if int64(actual) != 10000 {
		t.Errorf("actual_credits = %v, want 10000: with actuals unavailable the reservation hold is captured in full (#1215), never an undercharge", body["actual_credits"])
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must be false: no real usage arrived")
	}
}
