package inference

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- issue #1215: streaming settlement fails open ---
//
// Doctrine (D-034, owner ruling on #1215): a stream that delivered output is
// money collected, ALWAYS. When the upstream usage block is missing or
// unusable, settlement captures the reservation hold instead of undercharging
// from a content estimate or settling at zero, raises a loud alarm, and never
// misfiles a content-delivered stream as upstream_error.

// routeMockPricing mirrors the catalog row newRoutingMock answers with, so a
// test can price a settlement the way production does instead of hardcoding
// the arithmetic.
var routeMockPricing = SelectRouteResult{
	AliasID:   "gpt-4o",
	Provider:  "openrouter",
	Pricing:   catalogHiveFast,
	PriceUnit: PriceUnitTokens,
}

// contentOnlySSEServer streams an ordinary completion minus the terminal
// usage chunk: content, finish_reason stop, [DONE], no usage object anywhere.
// The shape of a provider that stopped honouring stream_options.include_usage,
// which is what the deepseek-v4-flash evidence in #1215 showed.
func contentOnlySSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		stop := "stop"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "a full answer the customer received ", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "in full, with no usage chunk behind it", &stop))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// unparseableFrameSSEServer streams frames that fail json.Unmarshal into
// ChatCompletionChunk (model typed as a number where a string is declared),
// then [DONE]. The typed relay drops these to the verbatim pass-through
// branch, so the caller receives every byte while the accumulator records
// nothing: the shape behind hive-free settling at zero as upstream_error
// "settle stream delivered nothing" (#1215).
func unparseableFrameSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			fmt.Fprintf(w, "data: {\"id\":\"c%d\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":42,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"piece %d \"},\"finish_reason\":null}]}\n", i, i)
			flusher.Flush()
		}
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// toolCallOnlySSEServer streams a turn whose deltas carry only tool-call
// payloads: parseable chunks that accumulate zero visible content, because
// AccumulateContent ignores tool-call deltas. A real billable agent turn that
// used to be filed as upstream_error with the hold released for free.
func toolCallOnlySSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"id":"t1","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Dha"}]}}]}`)
		fmt.Fprintln(w, `data: {"id":"t2","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// TestExecuteStreaming_ContentButNoUsageBlock_SettlesAtPricedCapture is the
// primary red test for #1215 family 1 (deepseek-v4-flash undercharge): content
// delivered, usage block absent. Settlement must charge, stay unconfirmed so
// control-plane reconciliation sees it, keep status completed, and raise the
// alarm.
//
// The figure it charges changed with #1198. This test used to require the
// reservation hold in full, and that requirement was itself the defect: the
// hold is a flat authorization floor, so on the live box it charged 100,000,000
// credits for turns whose median confirmed price on the same alias was 281.
// What #1215 actually needs is that a delivered stream is never undercharged to
// nothing, and the catalog price of the tokens involved satisfies that while
// the flat hold overshoots it by five orders of magnitude.
func TestExecuteStreaming_ContentButNoUsageBlock_SettlesAtPricedCapture(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec) // reservation EstimatedCredits 10000
	defer acctSrv.Close()

	litellmSrv := contentOnlySSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	reqBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write me a full answer about the sea, at length."}],"stream":true}`)
	var logs string
	logs = captureLogs(t, func() {
		done, _ := runExecuteStreamingWithBody(orch, context.Background(), reqBody)
		waitDone(t, done)
	})

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("content was delivered; expected FinalizeReservation, got calls: %+v", rec.calls)
	}
	actual := finalizeInt64(t, body, "actual_credits")
	// The whole content the mock streams, which is what the capture prices its
	// completion half from.
	const delivered = "a full answer the customer received " +
		"in full, with no usage chunk behind it"
	want := CreditsForTokens(routeMockPricing,
		estimateCompletionTokens(promptText(EndpointChatCompletions, reqBody)), 0, 0,
		estimateCompletionTokens(delivered))
	if actual > want {
		t.Errorf("actual_credits = %d, more than the %d this turn costs at catalog price: the hold is an authorization floor, never a measurement (#1198)", actual, want)
	}
	if actual < 1 {
		t.Errorf("actual_credits = %d: a delivered stream is never free (#1215, D-034)", actual)
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must be false: no real usage arrived, the charge routes to reconciliation")
	}
	if body["status"] != "completed" {
		t.Errorf("status = %v, want completed", body["status"])
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a delivered stream must never release its hold")
	}
	event, ok := rec.find("/internal/usage/events")
	if !ok || event["event_type"] != "completed" {
		t.Errorf("usage event type = %v, want completed", event["event_type"])
	}
	if !strings.Contains(logs, "stream_usage_block_missing") {
		t.Fatalf("no stream_usage_block_missing alarm in the log; logs:\n%s", logs)
	}
}

// TestExecuteStreaming_UnparseableFramesDelivered_CompletesNotUpstreamError is
// the red test for #1215 family 2 (hive-free zero settle): every byte reached
// the caller through the verbatim pass-through, yet nothing accumulated, so
// the old code settled zero and recorded upstream_error over a fully
// delivered response.
func TestExecuteStreaming_UnparseableFramesDelivered_CompletesNotUpstreamError(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := unparseableFrameSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	reqBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Tell me about the sea in several long paragraphs."}],"stream":true}`)
	var logs string
	logs = captureLogs(t, func() {
		done, _ := runExecuteStreamingWithBody(orch, context.Background(), reqBody)
		waitDone(t, done)
	})

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("the caller received full content; expected FinalizeReservation, got calls: %+v", rec.calls)
	}
	// Nothing accumulated, so there is no completion quantity to price and the
	// prompt carries the charge alone. It is an undercharge on the output half
	// and it is a deliberate one: the alternative this replaces was the flat
	// hold, which on the live box was 355,872x the alias price (#1198). Pricing
	// the forwarded bytes needs the relay to count them, which is tracked
	// separately rather than smuggled into a money fix.
	actual := finalizeInt64(t, body, "actual_credits")
	promptOnly := CreditsForTokens(routeMockPricing,
		estimateCompletionTokens(promptText(EndpointChatCompletions, reqBody)), 0, 0, 0)
	if actual != promptOnly {
		t.Errorf("actual_credits = %d, want %d: the prompt is the only quantity anything can price here (#1198)", actual, promptOnly)
	}
	if actual < 1 {
		t.Errorf("actual_credits = %d: a delivered stream is never free (#1215, D-034)", actual)
	}
	event, ok := rec.find("/internal/usage/events")
	if !ok || event["event_type"] != "completed" {
		t.Errorf("usage event type = %v, want completed: a content-delivered stream must never be filed as upstream_error", event["event_type"])
	}
	if !strings.Contains(logs, "stream_usage_block_missing") {
		t.Fatalf("no alarm marker in the log; logs:\n%s", logs)
	}
}

// TestExecuteStreaming_ToolCallOnlyTurn_Billed_NotUpstreamError covers
// the third family: a parseable stream whose deltas are all tool calls. No
// visible content accumulates, so the old code released the hold and filed
// upstream_error over a turn that ran real billable work.
func TestExecuteStreaming_ToolCallOnlyTurn_Billed_NotUpstreamError(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := toolCallOnlySSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	reqBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"What is the weather in Dhaka right now?"}],"stream":true}`)
	var logs string
	logs = captureLogs(t, func() {
		done, _ := runExecuteStreamingWithBody(orch, context.Background(), reqBody)
		waitDone(t, done)
	})

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("a served tool-calling turn is billable work; expected FinalizeReservation, got calls: %+v", rec.calls)
	}
	// The prompt AND the tool call the turn produced. AccumulateContent still
	// ignores tool-call deltas, because they are not text a customer can read,
	// but the settlement estimate no longer inherits that blindness: it prices
	// the tool call's own name and arguments as completion output (issue #928
	// defect 1). On this fixture the two figures round to the same credit,
	// because its tool call is a dozen bytes; the discriminating case, with an
	// argument payload of realistic size, is
	// TestExecuteStreaming_ToolCallOnlyTurn_ChargesForTheToolCallItProduced.
	actual := finalizeInt64(t, body, "actual_credits")
	want := CreditsForTokens(routeMockPricing,
		estimateCompletionTokens(promptText(EndpointChatCompletions, reqBody)), 0, 0,
		estimateCompletionTokens(`get_weather{"city":"Dha`))
	if actual != want {
		t.Errorf("actual_credits = %d, want %d: a served tool-calling turn bills the prompt it consumed plus the tool call it produced, never the flat hold (#1198, #928)", actual, want)
	}
	if actual < 1 {
		t.Errorf("actual_credits = %d: a served tool-calling turn is never free (#1215, D-034)", actual)
	}
	event, ok := rec.find("/internal/usage/events")
	if !ok || event["event_type"] != "completed" {
		t.Errorf("usage event type = %v, want completed", event["event_type"])
	}
	if !strings.Contains(logs, "stream_usage_block_missing") {
		t.Fatalf("no stream_usage_block_missing alarm in the log; logs:\n%s", logs)
	}
}

// TestExecuteStreaming_GenuineDeliveryFailure_StillReleasesAsUpstreamError is
// the regression guard the #1215 brief demands: the delivery fix must not
// swallow real failures. An upstream that dies before sending a single frame
// still releases in full as upstream_error.
func TestExecuteStreaming_GenuineDeliveryFailure_StillReleasesAsUpstreamError(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := abruptUpstreamCloseServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected ReleaseReservation when nothing was delivered; calls seen: %+v", rec.calls)
	}
	if body["reason"] != "upstream_error" {
		t.Errorf("reason = %v, want upstream_error", body["reason"])
	}
	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("nothing was delivered: must not finalize a charge")
	}
}
