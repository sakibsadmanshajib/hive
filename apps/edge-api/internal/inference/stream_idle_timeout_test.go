package inference

import (
	"context"
	"encoding/json"
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

// TestLiteLLMStreamingDispatchInstallsTheWatchdogOnTheBody pins WHERE the
// byte-level bound lives. Installed at the relay instead, it left two earlier
// readers of the same body unbounded: dispatchWithRetry's peekBody, which
// classifies a 404 or an allowance wall, and the relay's own non-2xx
// io.ReadAll. A 500 followed by silence hung in that gap forever (review
// finding on PR #1762). Installing it on the response body at dispatch covers
// every read from the moment the response exists.
func TestLiteLLMStreamingDispatchInstallsTheWatchdogOnTheBody(t *testing.T) {
	originalIdle := streamIdleTimeout
	streamIdleTimeout = 200 * time.Millisecond
	defer func() { streamIdleTimeout = originalIdle }()

	// A 500 whose body then never arrives. This is the exact shape that hung:
	// the status is not 2xx, so the relay reads the body with io.ReadAll to
	// build the provider-blind error, and dispatchWithRetry's peekBody has
	// already read it once to classify. Both happen before the relay loop.
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.(http.Flusher).Flush()
		select {
		case <-stop:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(stop)
		srv.Close()
	}()

	client := NewLiteLLMClient(srv.URL, "test-key")

	resp, err := client.ChatCompletionStream(context.Background(), "route", []byte(`{}`))
	if err != nil {
		t.Fatalf("streaming dispatch: %v", err)
	}
	defer resp.Body.Close()
	if _, ok := resp.Body.(*idleTimeoutReader); !ok {
		t.Errorf("streaming response body = %T, want the idle watchdog", resp.Body)
	}

	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = io.ReadAll(resp.Body)
	}()
	select {
	case <-read:
	case <-time.After(10 * time.Second):
		t.Fatal("reading a non-2xx body from a silent upstream never returned: installed at the relay instead of at dispatch, the watchdog does not cover this read or peekBody's (#928 defect 3, PR #1762 review)")
	}

	// The buffered client keeps its own total timeout and needs no watchdog;
	// two bounds on one read is one more than anything reads.
	buffered := NewLiteLLMClient("http://127.0.0.1:1", "test-key")
	if buffered.httpClient.Timeout <= 0 {
		t.Error("buffered client has no total timeout: nothing else bounds a buffered dispatch")
	}
}

// TestExecuteStreaming_KeepaliveOnlyUpstream_EndsTheRelayAndSettles is the
// second stall bound, and the one a byte-level watchdog cannot provide.
//
// A provider that sends an SSE comment every so often and never sends data
// resets any byte watchdog forever. Measured on the first version of this PR:
// forty ": ping" reads over two seconds against a 200ms budget, and the
// watchdog never fired (review finding). The consequence is not a slow request:
// the hold stays open until the reservation reaper reclaims it an hour later,
// and the reaper then releases a hold whose request is still live, leaving a
// served turn with no reservation behind it.
func TestExecuteStreaming_KeepaliveOnlyUpstream_EndsTheRelayAndSettles(t *testing.T) {
	originalIdle := streamIdleTimeout
	streamIdleTimeout = 200 * time.Millisecond
	defer func() { streamIdleTimeout = originalIdle }()

	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	stop := make(chan struct{})
	litellmSrv := keepaliveOnlySSEServer(stop, 50*time.Millisecond)
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
		t.Fatal("the relay never ended: a provider that sends only keepalives holds the reservation until the reaper, which then releases a hold whose request is still live (#928 defect 3)")
	}

	body := w.Body.String()
	if !strings.Contains(body, "partial answer") {
		t.Errorf("the content delivered before the keepalives must still reach the client; body:\n%s", body)
	}
	if !strings.Contains(body, `"stream_interrupted"`) {
		t.Errorf("expected the abort branch's provider-blind error frame once the data deadline passed; body:\n%s", body)
	}

	if rec.has("/internal/accounting/reservations/release") {
		t.Error("content was delivered before the stall: releasing in full would serve it free (D-034)")
	}
	fbody, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation after the data deadline; calls seen: %+v", rec.calls)
	}
	assertPricedCapture(t, fbody, mockReservationHold)
}

// keepaliveOnlySSEServer streams one ordinary content chunk and then nothing but
// SSE comments: bytes keep arriving, data never does.
func keepaliveOnlySSEServer(stop <-chan struct{}, interval time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "partial answer before the provider goes quiet", nil))
		flusher.Flush()
		for {
			select {
			case <-stop:
				return
			case <-r.Context().Done():
				return
			case <-time.After(interval):
			}
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}))
}

// TestStreamDataDeadline_KeepalivesDoNotRenewTheBudget is the unit-level twin:
// an SSE comment and a well-formed but empty chunk are both keepalives, and
// neither renews the budget. The second half matters as much as the first,
// because a provider whose keepalive is an empty chunk rather than a comment is
// the same starvation.
func TestStreamDataDeadline_KeepalivesDoNotRenewTheBudget(t *testing.T) {
	empty := ChatCompletionChunk{Choices: []ChunkChoice{{Index: 0}}}
	if chunkCarriesData(empty) {
		t.Error("a chunk with an empty delta and no finish reason carries no data: counting it renews the budget on a keepalive")
	}
	blank := ""
	if chunkCarriesData(ChatCompletionChunk{Choices: []ChunkChoice{{Delta: ChunkDelta{Content: &blank}}}}) {
		t.Error("an empty content delta carries no data")
	}

	text := "hello"
	for name, chunk := range map[string]ChatCompletionChunk{
		"content":     {Choices: []ChunkChoice{{Delta: ChunkDelta{Content: &text}}}},
		"refusal":     {Choices: []ChunkChoice{{Delta: ChunkDelta{Refusal: &text}}}},
		"tool call":   {Choices: []ChunkChoice{{Delta: ChunkDelta{ToolCalls: json.RawMessage(`[{"index":0}]`)}}}},
		"finish":      {Choices: []ChunkChoice{{FinishReason: &text}}},
		"usage block": {Usage: &UsageResponse{PromptTokens: 1}},
	} {
		if !chunkCarriesData(chunk) {
			t.Errorf("a %s frame carries data and must renew the budget", name)
		}
	}
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
