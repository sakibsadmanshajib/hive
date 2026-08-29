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
// data line larger than bufio.Scanner's configured max token size (see
// sseScanLineMaxBytes, the named constant both stream.go's and
// stream_responses.go's scanner.Buffer calls share). This is the exact shape
// issue #1255 HIGH #1 names: one oversized upstream line blows the scanner's
// buffer via bufio.ErrTooLong, not a slow drip of ordinary chunks. Shared by
// both the chat-completions and Responses API scanner-error regression tests
// below. The content is sized off sseScanLineMaxBytes, not a bare literal, so
// this fixture stays correct (still actually oversized) if that limit ever
// changes -- go-review MEDIUM 3 on PR #1271.
func oversizedLineSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "partial reply before the buffer blows up", nil))
		flusher.Flush()

		huge := strings.Repeat("x", sseScanLineMaxBytes+64*1024)
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
	// Captured and unconfirmed, at the catalog price of what was involved
	// rather than the flat hold (#1198). Nothing priceable survived the abort,
	// so the floor is the answer.
	assertPricedCapture(t, fbody, minimumCapture)
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

// TestExecuteStreaming_ClientDisconnect_NoSpuriousErrorFrame is the
// regression guard for go-review MEDIUM-1 on PR #1271: r.Context()
// cancellation tears down the in-flight upstream body read the exact same
// way a real relay failure does, so scanner.Err() == context.Canceled on an
// ordinary client disconnect too. Without the ctx.Err() == nil guard in
// stream.go, every routine cancellation (a user hitting stop) would log "SSE
// relay aborted" and write a spurious stream_interrupted frame to an
// already-dead socket, burying the real ErrTooLong signal this PR exists to
// surface under the far more common disconnect case.
//
// stream_disconnect_test.go's existing disconnect tests (reused here via
// gatedSSEServer/waitReady/waitDone/newHeaderCommitRecorder) never inspect
// w.Body, so they pass whether or not this bug exists; this test does
// inspect it, which is the only way to catch this class of regression.
func TestExecuteStreaming_ClientDisconnect_NoSpuriousErrorFrame(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	litellmSrv := gatedSSEServer("partial reply before the client gives up", ready)
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := newHeaderCommitRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(ctx, w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)
	}()

	waitReady(t, ready)
	cancel()
	waitDone(t, done)

	body := w.Body.String()
	if strings.Contains(body, "stream_interrupted") {
		t.Error("a client disconnect must never emit the stream_interrupted error frame: that would bury real relay failures under routine cancellations")
	}
}
