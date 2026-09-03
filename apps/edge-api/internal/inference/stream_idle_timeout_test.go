package inference

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- issue #928 defect 3: a 120 second TOTAL timeout covered the stream body ---
//
// http.Client.Timeout covers every byte of the response body, so any streamed
// turn still running two minutes after dispatch had its connection cut
// mid-stream. Buffered, that ceiling produced a clean 502 a client SDK can
// retry; streamed, it produced a truncation behind a committed 200, which the
// OpenHands SDK reassembles into a malformed or empty tool call.
//
// Removing it from the streaming path is only half the fix. The other half is
// that the hold must still be bounded, because a wedged provider that holds a
// connection open forever would otherwise hold a customer's credits with it.
// That is what streamIdleTimeout is for, and these tests pin both halves.

// TestLiteLLMStreamingDispatchHasNoTotalTimeout pins the split itself. A total
// timeout on a streaming exchange is a truncation waiting to happen, and the
// buffered client must keep one, because edge-api's own http.Server sets neither
// ReadTimeout nor WriteTimeout (they would cut SSE and large uploads), so for a
// buffered request this client timeout is the only total bound there is.
func TestLiteLLMStreamingDispatchHasNoTotalTimeout(t *testing.T) {
	client := NewLiteLLMClient("http://litellm:4000", "test-key")

	if client.streamClient.Timeout != 0 {
		t.Errorf("streaming client Timeout = %s, want 0: a total timeout cuts a healthy long answer mid-stream (#928 defect 3)",
			client.streamClient.Timeout)
	}
	if client.httpClient.Timeout <= 0 {
		t.Errorf("buffered client Timeout = %s, want a positive total budget: nothing else bounds a buffered dispatch",
			client.httpClient.Timeout)
	}
	transport, ok := client.streamClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("streaming client transport = %T, want *http.Transport carrying the per-phase bounds", client.streamClient.Transport)
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Error("streaming transport has no ResponseHeaderTimeout: an upstream that accepts the connection and never answers would hang forever")
	}
	if transport.TLSHandshakeTimeout <= 0 {
		t.Error("streaming transport has no TLSHandshakeTimeout")
	}
}

// stalledSSEServer streams one ordinary content chunk and then goes silent
// without closing the connection, which is what a wedged provider looks like
// from here: headers delivered, some output delivered, and then nothing, ever.
func stalledSSEServer(stop <-chan struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "partial answer before the provider wedges", nil))
		flusher.Flush()
		// Hold the connection open. Released when the test finishes or when the
		// relay's watchdog closes the body from the client side, whichever comes
		// first -- without the second arm httptest.Server.Close would block on
		// this handler.
		select {
		case <-stop:
		case <-r.Context().Done():
		}
	}))
}

// TestExecuteStreaming_StalledUpstream_EndsTheRelayAndSettles is the bound that
// replaces the total timeout. A provider that stops writing must not hold the
// customer's credits open: the idle watchdog closes the body, which surfaces as
// a scanner error and runs the relay's existing abort path, and settlement then
// charges for what was actually delivered.
func TestExecuteStreaming_StalledUpstream_EndsTheRelayAndSettles(t *testing.T) {
	originalIdle := streamIdleTimeout
	streamIdleTimeout = 150 * time.Millisecond
	defer func() { streamIdleTimeout = originalIdle }()

	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	// One defer for both, in this order on purpose. httptest.Server.Close waits
	// for outstanding handlers, and this fixture's handler blocks until either
	// stop closes or the client hangs up, so closing the server first would
	// deadlock the whole test binary on any failure path -- which is the path
	// where the failure message actually matters.
	stop := make(chan struct{})
	litellmSrv := stalledSSEServer(stop)
	defer func() {
		close(stop)
		litellmSrv.Close()
	}()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, mockReservationHold, false, nil,
			orch.litellm.ChatCompletionStream)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the relay never ended: a silent upstream holds the reservation open indefinitely (#928 defect 3)")
	}

	body := w.Body.String()
	if !strings.Contains(body, "partial answer") {
		t.Errorf("the content delivered before the stall must still reach the client; body:\n%s", body)
	}
	if !strings.Contains(body, `"stream_interrupted"`) {
		t.Errorf("expected the abort branch's provider-blind error frame after the stall; body:\n%s", body)
	}
	if strings.Contains(body, "data: [DONE]") {
		t.Error("a stalled stream must not claim a normal completion")
	}

	// The hold reaches a terminal state rather than sitting open: content was
	// delivered before the stall, so it is a priced capture and not a release.
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("content was delivered before the stall: releasing in full would serve it free (D-034)")
	}
	fbody, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation after the stall; calls seen: %+v", rec.calls)
	}
	assertPricedCapture(t, fbody, mockReservationHold)
}

// TestIdleTimeoutReader_ClosesAStalledBody is the watchdog on its own: a read
// that never returns is ended by closing the body underneath it, which is the
// mechanism the relay depends on to turn a stall into an ordinary read failure.
func TestIdleTimeoutReader_ClosesAStalledBody(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	reader := newIdleTimeoutReader(pr, 50*time.Millisecond, "test")
	defer reader.stop()

	start := time.Now()
	if _, err := reader.Read(make([]byte, 16)); err == nil {
		t.Fatal("a stalled read returned no error: the relay would keep waiting forever")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the stalled read took %s to fail, far past the 50ms idle budget", elapsed)
	}
	if !reader.trippedIdle() {
		t.Error("the watchdog did not record that it fired, so a stall is indistinguishable from any other read failure")
	}
}

// TestIdleTimeoutReader_DoesNotFireWhileBytesKeepArriving is the other
// direction, and the one that matters for the defect being fixed: a long answer
// that keeps streaming resets the budget on every read and runs as long as it
// needs to. A watchdog that fired on total elapsed time would just be the 120
// second total timeout again under another name.
func TestIdleTimeoutReader_DoesNotFireWhileBytesKeepArriving(t *testing.T) {
	pr, pw := io.Pipe()
	reader := newIdleTimeoutReader(pr, 100*time.Millisecond, "test")
	defer reader.stop()

	go func() {
		for i := 0; i < 10; i++ {
			if _, err := pw.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = pw.Close()
	}()

	buf := make([]byte, 1)
	for i := 0; i < 10; i++ {
		if _, err := reader.Read(buf); err != nil {
			t.Fatalf("read %d failed while data was still arriving: %v", i, err)
		}
	}
	if reader.trippedIdle() {
		t.Error("the watchdog fired on a stream that never went idle: a long answer would be cut exactly as the total timeout used to cut it")
	}
	_ = pr.Close()
}

// TestExecuteStreaming_NormalCompletion_UnaffectedByTheIdleWatchdog proves the
// watchdog changes nothing about an ordinary stream's wire output, the same
// property the scanner.Err() guard's own companion test pins.
func TestExecuteStreaming_NormalCompletion_UnaffectedByTheIdleWatchdog(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	stop := "stop"
	litellmSrv := mockSSEServer([]string{
		buildChunkLine("chunk-1", "route", "Hello", nil),
		buildChunkLine("chunk-2", "route", " world", &stop),
		"data: [DONE]",
	})
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
	if !strings.Contains(body, "Hello") || !strings.Contains(body, " world") {
		t.Errorf("ordinary content did not survive the relay; body:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("an ordinary stream must still end on [DONE]; body:\n%s", body)
	}
	if strings.Contains(body, `"stream_interrupted"`) {
		t.Errorf("the abort branch fired on a healthy stream; body:\n%s", body)
	}
}
