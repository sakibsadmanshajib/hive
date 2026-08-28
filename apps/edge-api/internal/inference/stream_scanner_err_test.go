package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// oversizedLineSSEServer streams one normal content chunk, then a single SSE
// data line larger than bufio.Scanner's configured max token size (512 KiB;
// see the scanner.Buffer call in stream.go and stream_responses.go). This is
// the exact shape issue #1255 HIGH #1 names: one oversized upstream line
// blows the scanner's buffer via bufio.ErrTooLong, not a slow drip of
// ordinary chunks. Shared by both the chat-completions and Responses API
// scanner-error regression tests below.
func oversizedLineSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "partial reply before the buffer blows up", nil))
		flusher.Flush()

		huge := strings.Repeat("x", 600*1024)
		chunk := ChatCompletionChunk{
			ID:     "chunk-2",
			Object: "chat.completion.chunk",
			Model:  "route",
			Choices: []ChunkChoice{
				{Index: 0, Delta: ChunkDelta{Content: &huge}},
			},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintln(w, "data: "+string(b))
		flusher.Flush()
	}))
}

// TestExecuteStreaming_OversizedUpstreamLine_ClientGetsHonestErrorFrame is the
// regression guard for issue #1255 HIGH #1: stream.go's relay loop never
// checked scanner.Err(), so a single upstream line over the 512 KiB token
// limit silently ended the relay -- no error frame, no [DONE], no log. This
// proves the client now sees an explicit, provider-blind error frame instead
// of a stream that just stops, and that billing settles exactly like the
// already-covered client-disconnect case (stream_disconnect_test.go): the
// reservation hold captured in full, flagged unconfirmed.
func TestExecuteStreaming_OversizedUpstreamLine_ClientGetsHonestErrorFrame(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := oversizedLineSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)

	body := w.Body.String()
	if !strings.Contains(body, `"stream_interrupted"`) {
		t.Fatalf("expected a client-visible stream_interrupted error frame; body:\n%s", body)
	}
	if strings.Contains(body, "data: [DONE]") {
		t.Error("an aborted stream must not also claim a normal [DONE] completion")
	}

	if rec.has("/internal/accounting/reservations/release") {
		t.Error("content was delivered before the failure: must not release in full")
	}
	fbody, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation despite the aborted stream; calls seen: %+v", rec.calls)
	}
	actual, _ := fbody["actual_credits"].(float64)
	if int64(actual) != 10000 {
		t.Errorf("actual_credits = %v, want 10000 (reservation hold captured in full, unconfirmed)", fbody["actual_credits"])
	}
	if confirmed, _ := fbody["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must be false: no real usage arrived before the abort")
	}
}

// TestExecuteStreaming_NormalCompletion_BodyUnaffectedByScannerErrCheck proves
// the new scanner.Err() branch does not fire, and changes nothing about the
// wire output, for an ordinary completion.
func TestExecuteStreaming_NormalCompletion_BodyUnaffectedByScannerErrCheck(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := completingSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)

	body := w.Body.String()
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "data: [DONE]") {
		t.Errorf("expected the stream to still end with data: [DONE]; body:\n%s", body)
	}
	if strings.Contains(body, "stream_interrupted") {
		t.Error("a normal completion must never emit the abort error frame")
	}
}
