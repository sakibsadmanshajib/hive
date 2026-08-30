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
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
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
//
// Each end-to-end case is built to go RED against one reverted production rule,
// and the comment on it names which. A case that passes whichever way the rule
// goes is filler, and three of the original six were exactly that.

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

// contentThenLengthSSEServer is the emptiness rule's other half: a stream that
// ran out of ceiling exactly like a burn (finish_reason=length, a confident
// usage block, [DONE]) and DID deliver visible text on the way. Every
// ingredient of the guard is present except the emptiness itself, so this is
// the fixture that exercises the content != "" clause. completingSSEServer,
// which the no-regression control used before, finishes on stop and is blocked
// by SawNonLengthFinish long before content is ever consulted.
func contentThenLengthSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		length := "length"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "a partial answer the customer read", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "", &length))
		flusher.Flush()
		fmt.Fprintln(w, "data: "+usageOnlyFrame(500, 300))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// toolCallTruncatedLengthSSEServer is the shape HasToolCall exists for and the
// only one where deleting it releases a real billable turn for free: an agent
// turn cut off at the caller's own ceiling mid tool-call arguments. It closes
// on finish_reason=length, not tool_calls, so SawNonLengthFinish does not block
// the guard, and it accumulates no visible text at all because
// AccumulateContent ignores tool-call deltas. toolCallOnlySSEServer, which the
// original test used, finishes on tool_calls and is blocked by
// SawNonLengthFinish before HasToolCall is read.
func toolCallTruncatedLengthSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"id":"t1","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Dha"}}]}}]}`)
		flusher.Flush()
		fmt.Fprintln(w, `data: {"id":"t2","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
		flusher.Flush()
		fmt.Fprintln(w, "data: "+usageOnlyFrame(500, 300))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// refusalOnlyLengthSSEServer streams a refusal and nothing else, closing on
// finish_reason=length. A refusal is generated assistant output the caller can
// read and act on, so this is a delivered response and bills.
//
// It matters most on the Responses relay, which is what this fixture is used
// for. The chat relay folds delta.refusal into the content builder, so there
// the refusal is already visible text; the Responses translator writes only
// delta.content into currentContent, because that same builder is what its
// caller-visible output_text events are emitted from, so without
// HasVisibleRefusal a refusal-only Responses stream looks byte-identical to a
// burn and releases for free.
func refusalOnlyLengthSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"id":"r1","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{"role":"assistant","refusal":"I cannot help with that."}}]}`)
		flusher.Flush()
		fmt.Fprintln(w, `data: {"id":"r2","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
		flusher.Flush()
		fmt.Fprintln(w, "data: "+usageOnlyFrame(500, 300))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// abortAfterLengthSSEServer delivers the whole burn shape and then emits a
// single line past the scanner's 512 KiB limit, so the relay ends on
// bufio.ErrTooLong instead of [DONE]. The finish_reason has already arrived, so
// every other ingredient of the guard is satisfied; what is not known is what
// the frame that failed to scan contained, and it could have carried the entire
// visible answer.
func abortAfterLengthSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		length := "length"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "", &length))
		flusher.Flush()
		fmt.Fprintf(w, "data: %s\n", strings.Repeat("x", sseScanLineMaxBytes+1))
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

// newAccountingMockReleaseFails behaves like newAccountingMock but answers
// every release call with a 500. release is the ONLY terminal accounting call
// the zero-content path makes, so without this variant the branch where it
// fails (settleStream returns false, no usage event is written, the hold waits
// for the TTL reaper) is unreachable by any stub in this package. A billing
// stub that cannot express failure is how #1466's lookup-error branch reached
// zero coverage.
func newAccountingMockReleaseFails(rec *accountingRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		if r.URL.Path == "/internal/accounting/reservations/release" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/internal/accounting/reservations":
			_ = json.NewEncoder(w).Encode(ReservationResult{
				ID: "res-test-1", AccountID: "acct-test-1", Status: "active", EstimatedCredits: mockReservationHold,
			})
		case "/internal/usage/attempts":
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "streaming",
			})
		}
	}))
}

// zeroContentFixture wires the shared streaming harness against a caller-chosen
// upstream and returns the recorder holding every accounting call settlement
// made.
func zeroContentFixture(t *testing.T, upstream *httptest.Server) *accountingRecorder {
	t.Helper()
	return zeroContentFixtureWithAccounting(t, upstream, newAccountingMock)
}

// zeroContentFixtureWithAccounting is zeroContentFixture with a caller-chosen
// accounting stub, so a test can drive the release-failure branch.
func zeroContentFixtureWithAccounting(t *testing.T, upstream *httptest.Server, mock func(*accountingRecorder) *httptest.Server) *accountingRecorder {
	t.Helper()
	rec := &accountingRecorder{}
	acctSrv := mock(rec)
	t.Cleanup(acctSrv.Close)
	routingSrv := newRoutingMock(upstream.URL)
	t.Cleanup(routingSrv.Close)
	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)
	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)
	return rec
}

// absorbedCredits reads the money counter's outcome series. Read as a delta
// across the call under test, never as an absolute: these counters are
// package-level and every test in this file adds to the same two series.
func absorbedCredits(outcome string) int64 {
	return int64(testutil.ToFloat64(streamZeroContentAbsorbedCredits.WithLabelValues(outcome)))
}

// burnCredits is the catalog price of the tokens every burn fixture here
// reports, which is both the charge a billed variant must land on and the
// figure an absorbed variant must record as the cost Hive carried. Derived from
// the same CreditsForTokens the settlement uses rather than hardcoded, so it
// cannot drift away from the price the mock route actually carries.
func burnCredits(prompt, completion int64) int64 {
	return CreditsForTokens(routeMockPricing, prompt, 0, 0, completion)
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

// assertChargedExactly asserts the amount AND its provenance on the same
// finalize call: what was charged, and what evidence the charge was derived
// from. A floor assertion (actual_credits >= 1) cannot do this, because the
// capture path is already floored at one credit, so the assertion held for any
// figure the code could produce including a one-credit undercharge.
func assertChargedExactly(t *testing.T, rec *accountingRecorder, want int64, wantConfirmed bool) {
	t.Helper()
	if rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("a delivered stream released its hold instead of charging it; calls seen %+v", rec.calls)
	}
	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("a delivered stream never finalized; calls seen %+v", rec.calls)
	}
	if got := finalizeInt64(t, body, "actual_credits"); got != want {
		t.Errorf("actual_credits = %d, want exactly %d (the catalog price of the tokens the upstream reported)", got, want)
	}
	if got, _ := body["terminal_usage_confirmed"].(bool); got != wantConfirmed {
		t.Errorf("terminal_usage_confirmed = %v, want %v: the amount above is only meaningful paired with the evidence it came from", got, wantConfirmed)
	}
}

// The defect itself. A well-formed stream carrying no assistant-visible text in
// any chunk must not become a charge, however confident the upstream's usage
// block is about the tokens it burned getting there.
//
// Goes red if the guard is removed, and also if the absorbed cost stops being
// recorded: the release alone says a burn happened, and the counter is the only
// place that says what it cost.
func TestExecuteStreaming_ZeroVisibleContent_ReleasesHoldWithoutCharging(t *testing.T) {
	upstream := emptyLengthSSEServer()
	defer upstream.Close()

	before := absorbedCredits(zeroContentOutcomeReleased)
	rec := zeroContentFixture(t, upstream)
	assertHoldReleasedNotCharged(t, rec, "zero_content")

	// The money, not merely the fact that a release happened. This is the
	// figure an operator sums to answer "how much did we absorb yesterday".
	want := burnCredits(500, 300)
	if got := absorbedCredits(zeroContentOutcomeReleased) - before; got != want {
		t.Errorf("absorbed credits recorded = %d, want %d (the catalog price of the 500 prompt and 300 completion tokens the upstream reported burning)", got, want)
	}

	// The customer's own row must not carry a spend for a request the customer
	// was not charged for. hive_credit_delta is the customer's signed spend and
	// an absorbed burn is Hive's cost, so 0 is the honest value here; the cost
	// Hive carried is recorded above, as Hive's.
	event, ok := rec.find("/internal/usage/events")
	if !ok {
		t.Fatalf("no usage event was recorded for the absorbed burn; calls seen %+v", rec.calls)
	}
	if delta, _ := event["hive_credit_delta"].(float64); delta != 0 {
		t.Errorf("hive_credit_delta = %v on an absorbed burn, want 0: the customer paid nothing", delta)
	}
}

// No-regression control: a stream that ran out of ceiling exactly like a burn
// and DID deliver visible text still bills, at the catalog price of the tokens
// the upstream reported, off a confirmed usage block.
//
// Goes red if the content != "" clause is dropped from the guard.
func TestExecuteStreaming_ContentDelivered_StillBillsNormally(t *testing.T) {
	upstream := contentThenLengthSSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	assertChargedExactly(t, rec, burnCredits(500, 300), true)
}

// A tool-call-only turn carries no assistant-visible text and is not empty: it
// is a complete, useful response, and the customer pays for it. This is the
// truncated-mid-arguments shape, which closes on length rather than tool_calls,
// so HasToolCall is the only thing standing between it and a free release.
//
// Goes red if HasToolCall is dropped from the guard or from ObserveShape.
func TestExecuteStreaming_ToolCallOnly_IsNotEmptyAndBills(t *testing.T) {
	upstream := toolCallTruncatedLengthSSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	assertChargedExactly(t, rec, burnCredits(500, 300), true)
}

// finish_reason=stop with empty text is a genuinely empty answer the upstream
// called complete, not the reasoning-burn signature. The sync guard has always
// declined to rewrite that settlement and this one declines too: the guard
// needs positive evidence of the burn shape, and absent it the request bills
// (D-034, fail closed).
//
// Goes red if SawNonLengthFinish stops blocking the guard.
func TestExecuteStreaming_EmptyButFinishedStop_StillBills(t *testing.T) {
	upstream := emptyStopSSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	assertChargedExactly(t, rec, burnCredits(500, 12), true)
}

// cancelOnDoneRecorder cancels the request context the instant the [DONE]
// sentinel is written to the caller, which is what a real client does to a
// stream that said nothing: it reads the sentinel and closes the tab. net/http
// cancels r.Context() on that close, and the settlement defer is still on the
// stack when it happens.
type cancelOnDoneRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancelOnDoneRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(b)
	if strings.Contains(string(b), "[DONE]") {
		r.once.Do(r.cancel)
	}
	return n, err
}

// The CRITICAL case, and the one an earlier draft of this guard got backwards.
// A blank stream is exactly what a caller hangs up on, so the commonest ending
// of a reasoning burn is a client close milliseconds after [DONE]. That close
// cancels r.Context() while settlement is still running. A guard that read the
// request context at settlement time was therefore suppressed in precisely the
// case it exists for, and billed the burn; the same cancellation arrives from a
// Caddy reset, an http.Server WriteTimeout or a graceful shutdown, none of
// which are the caller at all.
//
// The stream here is complete: every frame arrived, [DONE] arrived, and only
// then did the socket go. It is a burn and it is absorbed.
//
// Goes red the moment settlement consults the caller's context to decide
// emptiness. Deterministic rather than timing-based: the cancel fires inside
// the [DONE] write itself.
func TestExecuteStreaming_ClientClosedRightAfterDone_BurnStillAbsorbed(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	upstream := emptyLengthSSEServer()
	defer upstream.Close()

	routingSrv := newRoutingMock(upstream.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &cancelOnDoneRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(ctx, w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)
	}()
	waitDone(t, done)

	if ctx.Err() == nil {
		t.Fatal("sanity check failed: the client never actually disconnected, so this proves nothing")
	}
	assertHoldReleasedNotCharged(t, rec, "zero_content")
}

// cancelOnUpstreamDoneReader cancels the request context the instant the
// relay's own read of the upstream's [DONE] sentinel returns, which lands
// before the relay loop's break statement even runs -- and so, well before
// the post-loop completeness check a few lines below it (stream.go's own
// "9c"). This is the read-side mirror of cancelOnDoneRecorder above, which
// hooks the OUTBOUND write of [DONE] to the caller instead: here the cancel
// fires on the INBOUND read of the upstream's [DONE], which is the earlier of
// the two events on every stream that completes at all.
type cancelOnUpstreamDoneReader struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancelOnUpstreamDoneReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && strings.Contains(string(p[:n]), "[DONE]") {
		r.once.Do(r.cancel)
	}
	return n, err
}

// dispatchCancelingOnUpstreamDone wraps a real dispatchFunc so the response
// body it returns cancels ctx the moment the upstream's own [DONE] sentinel
// is read off the wire, instead of only when this gateway's own [DONE] later
// reaches the client. See cancelOnUpstreamDoneReader.
func dispatchCancelingOnUpstreamDone(dispatch dispatchFunc, cancel context.CancelFunc) dispatchFunc {
	return func(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
		resp, err := dispatch(ctx, litellmModel, body)
		if err != nil {
			return resp, err
		}
		resp.Body = &cancelOnUpstreamDoneReader{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
}

// The [DONE]-branch setter's own regression guard (stream.go's
// `accumulator.StreamCompleted = true` inside `if line == "data: [DONE]"`).
// Deleting only that line leaves the rest of this file green, because every
// other fixture here still has a live ctx when the post-loop completeness
// check runs a few lines later and sets the same field itself. The two
// setters diverge in exactly one population, and it is the one #1326 exists
// for: a caller whose context is already cancelled by the time the read loop
// exits an empty stream.
//
// TestExecuteStreaming_ClientClosedRightAfterDone_BurnStillAbsorbed above
// cancels on the OUTBOUND write of [DONE] to the caller, which happens after
// the post-loop check already ran and set StreamCompleted itself -- it does
// not pin the [DONE]-branch setter. This test cancels on the INBOUND read of
// the upstream's own [DONE] instead, before the relay loop's break, so ctx is
// already cancelled by the time the post-loop check runs, its own
// ctx.Err()==nil guard fails, and the [DONE]-branch setter is the only one
// that ran.
//
// Goes red if stream.go's [DONE]-branch StreamCompleted=true is deleted: with
// it gone, StreamCompleted stays false, isZeroContentStream returns false,
// and the burn bills in full instead of being absorbed.
func TestExecuteStreaming_UpstreamDoneReadBeforeClientCancel_BurnStillAbsorbed(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	upstream := emptyLengthSSEServer()
	defer upstream.Close()

	routingSrv := newRoutingMock(upstream.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatch := dispatchCancelingOnUpstreamDone(orch.litellm.ChatCompletion, cancel)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(ctx, w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, dispatch)
	}()
	waitDone(t, done)

	if ctx.Err() == nil {
		t.Fatal("sanity check failed: the upstream's [DONE] was never read, so this proves nothing")
	}
	assertHoldReleasedNotCharged(t, rec, "zero_content")
}

// gatedEmptyLengthSSEServer streams the full reasoning-burn shape -- an empty
// content frame and a finish_reason=length frame, no usage block -- and then
// keeps the connection open until the caller's context dies. There is no
// [DONE]: this stream was cut off mid-flight, which is what tells it apart from
// the completed burn above.
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

// The fail-closed direction. Every ingredient of the emptiness rule is present
// here -- no visible text, no tool call, a finish_reason=length -- and the
// stream was still cut off before its own end. The frames that never arrived
// are unknown, and unknown bills (D-034).
//
// Goes red if StreamCompleted stops being required, which is the change that
// would make the two abandonment directions collapse back into one.
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

	if rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("a truncated stream released its hold as a burn; calls seen %+v", rec.calls)
	}
	if _, ok := rec.find("/internal/accounting/reservations/finalize"); !ok {
		t.Fatalf("a truncated stream never settled at all; calls seen %+v", rec.calls)
	}
}

// The same completeness rule from the upstream's side. The relay aborted on a
// single oversized line (bufio.ErrTooLong) AFTER the finish_reason had already
// arrived, so SawFinish is true and the burn shape looks complete. It is not:
// the frame that failed to scan is by definition the one whose contents are
// unknown, and it could have carried the whole visible answer.
//
// Goes red if StreamCompleted is set anywhere other than a genuine end of
// stream, for instance from SawFinish or from the relay's own exit.
func TestExecuteStreaming_RelayAbortedAfterLengthFinish_StillBills(t *testing.T) {
	upstream := abortAfterLengthSSEServer()
	defer upstream.Close()

	rec := zeroContentFixture(t, upstream)
	if rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("an aborted relay released its hold as a completed burn; calls seen %+v", rec.calls)
	}
	if _, ok := rec.find("/internal/accounting/reservations/finalize"); !ok {
		t.Fatalf("an aborted relay never settled at all; calls seen %+v", rec.calls)
	}
}

// The release is the only terminal accounting call this path makes, and when it
// fails nothing durable is written at all: no usage event, and a hold left for
// the TTL reaper to reclaim under its own reason. So the absorption must not be
// counted as one. The counter records outcomes, not intentions.
//
// Goes red if the increment moves back above the release call.
func TestExecuteStreaming_ZeroContent_ReleaseFails_NotCountedAsAbsorbed(t *testing.T) {
	upstream := emptyLengthSSEServer()
	defer upstream.Close()

	beforeReleased := absorbedCredits(zeroContentOutcomeReleased)
	beforeFailed := absorbedCredits(zeroContentOutcomeReleaseFailed)

	rec := zeroContentFixtureWithAccounting(t, upstream, newAccountingMockReleaseFails)

	if !rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("the release was never attempted; calls seen %+v", rec.calls)
	}
	if rec.has("/internal/usage/events") {
		t.Error("a usage event was written for a burn whose release failed: settleStream returns before recording one, and a test that saw one would mean the ordering changed silently")
	}
	if got := absorbedCredits(zeroContentOutcomeReleased) - beforeReleased; got != 0 {
		t.Errorf("absorbed credits under outcome=released moved by %d on a release that failed, want 0: this series is what an operator sums, and a failed settlement is not an absorption that landed", got)
	}
	want := burnCredits(500, 300)
	if got := absorbedCredits(zeroContentOutcomeReleaseFailed) - beforeFailed; got != want {
		t.Errorf("absorbed credits under outcome=release_failed = %d, want %d: a burn that could not settle must still be visible, under its own outcome", got, want)
	}
}

// Both relays feed the same guard, and only the chat one had any end-to-end
// coverage. The Responses relay accumulates visible text into its own builder
// and never calls AccumulateContent, so its emptiness can differ from the chat
// path's on the same upstream frames.
//
// Goes red if ObserveShape or the StreamCompleted marking is dropped from
// executeResponsesStreaming.
func TestExecuteResponsesStreaming_ZeroVisibleContent_ReleasesHoldWithoutCharging(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	upstream := emptyLengthSSEServer()
	defer upstream.Close()

	routingSrv := newRoutingMock(upstream.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)

	before := absorbedCredits(zeroContentOutcomeReleased)
	runResponsesStreaming(t, orch, context.Background())

	assertHoldReleasedNotCharged(t, rec, "zero_content")
	want := burnCredits(500, 300)
	if got := absorbedCredits(zeroContentOutcomeReleased) - before; got != want {
		t.Errorf("absorbed credits recorded = %d, want %d: the Responses relay must record the same money the chat relay does", got, want)
	}
}

// A refusal is assistant-visible output and bills. On the Responses relay it is
// also the one shape where the two relays genuinely disagree about what visible
// text is, since currentContent holds only delta.content.
//
// Goes red if HasVisibleRefusal is dropped from the guard or from ObserveShape.
func TestExecuteResponsesStreaming_RefusalOnly_IsNotEmptyAndBills(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	upstream := refusalOnlyLengthSSEServer()
	defer upstream.Close()

	routingSrv := newRoutingMock(upstream.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, upstream.URL)
	runResponsesStreaming(t, orch, context.Background())

	assertChargedExactly(t, rec, burnCredits(500, 300), true)
}

// runResponsesStreaming drives one executeResponsesStreaming call to
// completion, the same shape stream_responses_disconnect_test.go uses.
func runResponsesStreaming(t *testing.T, orch *Orchestrator, ctx context.Context) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		orch.executeResponsesStreaming(ctx, w, req, []byte(`{}`), ResponsesRequest{Model: "gpt-4o"}, "gpt-4o",
			NeedFlags{NeedResponses: true, NeedStreaming: true}, 10000)
	}()
	waitDone(t, done)
}

// The rule itself, in one table. The end-to-end tests prove the wiring; this
// proves the boundary conditions, including the ones no upstream mock
// conveniently produces.
func TestIsZeroContentStream(t *testing.T) {
	length, stop, toolCalls := "length", "stop", "tool_calls"
	refusal := "I cannot help with that."
	// completed builds an accumulator for a stream that reached its own end of
	// stream, which is the state settlement judges. truncated is the same
	// frames without that.
	truncated := func(reason *string) *UsageAccumulator {
		acc := &UsageAccumulator{}
		acc.ObserveShape(ChatCompletionChunk{Choices: []ChunkChoice{{FinishReason: reason}}})
		return acc
	}
	completed := func(reason *string) *UsageAccumulator {
		acc := truncated(reason)
		acc.StreamCompleted = true
		return acc
	}
	withToolCall := func(reason *string) *UsageAccumulator {
		acc := &UsageAccumulator{}
		acc.ObserveShape(ChatCompletionChunk{Choices: []ChunkChoice{{
			Delta:        ChunkDelta{ToolCalls: json.RawMessage(`[{"index":0}]`)},
			FinishReason: reason,
		}}})
		acc.StreamCompleted = true
		return acc
	}
	withRefusal := func(reason *string) *UsageAccumulator {
		acc := &UsageAccumulator{}
		acc.ObserveShape(ChatCompletionChunk{Choices: []ChunkChoice{{
			Delta:        ChunkDelta{Refusal: &refusal},
			FinishReason: reason,
		}}})
		acc.StreamCompleted = true
		return acc
	}
	mixedFinish := func() *UsageAccumulator {
		acc := &UsageAccumulator{}
		acc.ObserveShape(ChatCompletionChunk{Choices: []ChunkChoice{
			{Index: 0, FinishReason: &length},
			{Index: 1, FinishReason: &stop},
		}})
		acc.StreamCompleted = true
		return acc
	}

	cases := []struct {
		name    string
		acc     *UsageAccumulator
		content string
		want    bool
	}{
		{"reasoning burn: empty, finished on length, stream completed", completed(&length), "", true},
		{"visible text of any length", completed(&length), "a", false},
		{"same burn shape, but the stream never completed", truncated(&length), "", false},
		{"tool call finished on length is real work", withToolCall(&length), "", false},
		{"tool call finished on tool_calls is real work", withToolCall(&toolCalls), "", false},
		{"a refusal is visible output the caller can act on", withRefusal(&length), "", false},
		{"finished on stop, upstream called it complete", completed(&stop), "", false},
		{"relay cut off before any finish_reason", &UsageAccumulator{StreamCompleted: true}, "", false},
		{"one choice of two finished on something else", mixedFinish(), "", false},
		{"no accumulator at all", nil, "", false},
	}
	for _, tc := range cases {
		if got := isZeroContentStream(tc.acc, tc.content); got != tc.want {
			t.Errorf("%s: isZeroContentStream = %v, want %v", tc.name, got, tc.want)
		}
	}
}
