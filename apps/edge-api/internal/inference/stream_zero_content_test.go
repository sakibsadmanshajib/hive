package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- streaming zero-content guard (issue #1326) ---
//
// The sync path has had a zero-content guard since #1171: a completion whose
// every choice finished finish_reason=length with no visible output is the
// reasoning-burn signature, and it is retried once and then settled as a
// bounded capture. The streaming path had nothing. A stream in that shape
// relayed a well-formed sequence of chat.completion.chunk frames, none of them
// carrying a single character for the customer to read, and then settled at
// full catalog price off the upstream's own usage block. The customer watched
// a response that said nothing and paid for it, with nothing anywhere in the
// response saying anything had gone wrong.
//
// Every case here is constructed at the boundary with a fabricated upstream
// rather than by hunting a live pool member: the defect is intermittent
// upstream behaviour, so a test that waits for a real one would pass for the
// wrong reason on every run that failed to catch it.

// emptyLengthSSEServer streams the reasoning-burn shape: a role frame, an
// empty-content frame, a finish_reason=length frame, a terminal usage frame
// reporting real prompt and completion tokens, and [DONE]. Every frame is a
// valid chat.completion.chunk. Not one of them carries a character of
// assistant-visible text.
func emptyLengthSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		length := "length"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-3", "route", "", &length))
		flusher.Flush()
		fmt.Fprintln(w, "data: "+usageOnlyFrame(500, 300))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// emptyStopSSEServer is emptyLengthSSEServer with finish_reason=stop. An
// upstream that calls a response complete is making a different claim from one
// that ran out of ceiling mid-reasoning, and the guard deliberately does not
// second-guess it.
func emptyStopSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		stop := "stop"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "", &stop))
		flusher.Flush()
		fmt.Fprintln(w, "data: "+usageOnlyFrame(500, 12))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// usageOnlyFrame builds the terminal usage frame an OpenAI-compatible upstream
// sends for stream_options.include_usage: no choices, real token counts.
func usageOnlyFrame(prompt, completion int64) string {
	b, _ := json.Marshal(ChatCompletionChunk{
		ID:      "chunk-usage",
		Object:  "chat.completion.chunk",
		Created: 1700000000,
		Model:   "route",
		Choices: []ChunkChoice{},
		Usage: &UsageResponse{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			CompletionTokensDetails: &CompletionTokensDetails{
				ReasoningTokens: completion,
			},
		},
	})
	return string(b)
}

// zeroContentFixture wires the shared streaming harness against a caller-chosen
// upstream and returns the recorder holding every accounting call settlement
// made.
func zeroContentFixture(t *testing.T, upstream *httptest.Server) *accountingRecorder {
	t.Helper()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	t.Cleanup(acctSrv.Close)
	routingSrv := newRoutingMock(upstream.URL)
	t.Cleanup(routingSrv.Close)
	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)
	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)
	return rec
}

// assertHoldReleasedNotCharged asserts the terminal state of a hold that must
// not become a charge: exactly one release, no finalize at all. Asserting both
// halves is the point -- a release that ran alongside a finalize would leave
// the customer charged, and a finalize alone would leave nothing released.
func assertHoldReleasedNotCharged(t *testing.T, rec *accountingRecorder, wantReason string) {
	t.Helper()
	if body, ok := rec.find("/internal/accounting/reservations/finalize"); ok {
		t.Fatalf("a stream that delivered no visible content was charged: finalize call was %v", body)
	}
	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("the hold reached no terminal state at all: calls seen %+v", rec.calls)
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("release reservation_id = %v, want res-test-1", body["reservation_id"])
	}
	if got, _ := body["reason"].(string); got != wantReason {
		t.Errorf("release reason = %q, want %q so the release is attributable in the ledger", got, wantReason)
	}
}

// assertChargedAtLeast asserts the hold became a charge of at least one credit
// and was never also released, the terminal state every delivered stream must
// reach.
func assertChargedAtLeast(t *testing.T, rec *accountingRecorder, min int64) map[string]any {
	t.Helper()
	if rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("a delivered stream released its hold instead of charging it; calls seen %+v", rec.calls)
	}
	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("a delivered stream never finalized; calls seen %+v", rec.calls)
	}
	if credits := finalizeInt64(t, body, "actual_credits"); credits < min {
		t.Errorf("actual_credits = %d, want at least %d: a delivered stream is never free (D-034, D-055)", credits, min)
	}
	return body
}

// The defect itself. A well-formed stream carrying no assistant-visible text in
// any chunk must not become a charge, however confident the upstream's usage
// block is about the tokens it burned getting there.
func TestExecuteStreaming_ZeroVisibleContent_ReleasesHoldWithoutCharging(t *testing.T) {
	upstream := emptyLengthSSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	assertHoldReleasedNotCharged(t, rec, "zero_content")
}

// No-regression control: an ordinary stream with content still bills, off a
// confirmed usage block, at the catalog price rather than a capture.
func TestExecuteStreaming_ContentDelivered_StillBillsNormally(t *testing.T) {
	upstream := completingSSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	body := assertChargedAtLeast(t, rec, 1)
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); !confirmed {
		t.Error("terminal_usage_confirmed = false on a stream whose upstream sent a real usage block")
	}
}

// A tool-call-only turn carries no assistant-visible text and is not empty: it
// is a complete, useful response, and the customer pays for it. The rule keys
// on visible text, so this case exists to prove the rule does not swallow it.
// The upstream is the one stream_usage_missing_test.go already uses for the
// same shape: parseable frames, a tool-call delta, no usage block at all.
func TestExecuteStreaming_ToolCallOnly_IsNotEmptyAndBills(t *testing.T) {
	upstream := toolCallOnlySSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	assertChargedAtLeast(t, rec, 1)
}

// finish_reason=stop with empty text is a genuinely empty answer the upstream
// called complete, not the reasoning-burn signature. The sync guard has always
// declined to rewrite that settlement and this one declines too: the guard
// needs positive evidence of the burn shape, and absent it the request bills
// (D-034, fail closed).
func TestExecuteStreaming_EmptyButFinishedStop_StillBills(t *testing.T) {
	upstream := emptyStopSSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	assertChargedAtLeast(t, rec, 1)
}

// The correctness crux. A client that hangs up is not a reasoning burn, and
// what it received before hanging up is billable. reqCtx cancellation is the
// signal that tells the two apart, and it is checked before the emptiness rule
// so an abandoned stream can never be mistaken for an empty one.
func TestExecuteStreaming_ClientAbandonedAfterPartialContent_StillBills(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	upstream := gatedSSEServer("two tokens the client did receive", ready)
	defer upstream.Close()

	routingSrv := newRoutingMock(upstream.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done, _ := runExecuteStreaming(orch, ctx)
	waitReady(t, ready)
	cancel()
	waitDone(t, done)

	assertChargedAtLeast(t, rec, 1)
}

// The rule itself, in one table. The end-to-end tests prove the wiring; this
// proves the boundary conditions, including the ones no upstream mock
// conveniently produces.
func TestIsZeroContentStream(t *testing.T) {
	length, stop, toolCalls := "length", "stop", "tool_calls"
	finished := func(reason *string) *UsageAccumulator {
		acc := &UsageAccumulator{}
		acc.ObserveShape(ChatCompletionChunk{Choices: []ChunkChoice{{FinishReason: reason}}})
		return acc
	}
	withToolCall := func(reason *string) *UsageAccumulator {
		acc := &UsageAccumulator{}
		acc.ObserveShape(ChatCompletionChunk{Choices: []ChunkChoice{{
			Delta:        ChunkDelta{ToolCalls: json.RawMessage(`[{"index":0}]`)},
			FinishReason: reason,
		}}})
		return acc
	}
	mixedFinish := func() *UsageAccumulator {
		acc := &UsageAccumulator{}
		acc.ObserveShape(ChatCompletionChunk{Choices: []ChunkChoice{
			{Index: 0, FinishReason: &length},
			{Index: 1, FinishReason: &stop},
		}})
		return acc
	}

	cases := []struct {
		name       string
		acc        *UsageAccumulator
		content    string
		clientGone bool
		want       bool
	}{
		{"reasoning burn: empty, finished on length", finished(&length), "", false, true},
		{"visible text of any length", finished(&length), "a", false, false},
		{"client hung up on the same shape", finished(&length), "", true, false},
		{"tool call finished on length is real work", withToolCall(&length), "", false, false},
		{"tool call finished on tool_calls is real work", withToolCall(&toolCalls), "", false, false},
		{"finished on stop, upstream called it complete", finished(&stop), "", false, false},
		{"relay cut off before any finish_reason", &UsageAccumulator{}, "", false, false},
		{"one choice of two finished on something else", mixedFinish(), "", false, false},
		{"no accumulator at all", nil, "", false, false},
	}
	for _, tc := range cases {
		if got := isZeroContentStream(tc.acc, tc.content, tc.clientGone); got != tc.want {
			t.Errorf("%s: isZeroContentStream = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// gatedEmptyLengthSSEServer streams the full reasoning-burn shape -- an empty
// content frame and a finish_reason=length frame, no usage block -- and then
// keeps the connection open until the caller's context dies. It exists to build
// the one case where every ingredient of the emptiness rule is present and the
// client hung up anyway.
func gatedEmptyLengthSSEServer(ready chan<- struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		length := "length"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "", &length))
		flusher.Flush()
		// Same deterministic window gatedSSEServer uses: flushing here proves
		// only that the server sent the bytes, not that the relay goroutine has
		// been scheduled to read them.
		time.Sleep(50 * time.Millisecond)
		close(ready)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
}

// The same distinction from the other side, and the fail-closed half of it.
// Every ingredient of the emptiness rule is present here -- no visible text, no
// tool call, a finish_reason=length -- and the client still hung up. A gateway
// that cannot tell which of the two produced the silence must bill, so the
// cancelled request context is checked before the emptiness rule and wins.
func TestExecuteStreaming_ClientAbandonedEmptyLengthStream_StillBills(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	upstream := gatedEmptyLengthSSEServer(ready)
	defer upstream.Close()

	routingSrv := newRoutingMock(upstream.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done, _ := runExecuteStreaming(orch, ctx)
	waitReady(t, ready)
	cancel()
	waitDone(t, done)

	assertChargedAtLeast(t, rec, 1)
}
