package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- issue #1198: the fail-closed hold capture billed the flat hold ---
//
// Measured on the live demo box on 2026-08-29T08:07Z: seventeen reservations
// were charged exactly 100,000,000 credits, the whole DefaultHoldText
// authorization floor, with terminal_usage_confirmed false. Against the median
// confirmed charge on the same alias that is 355,872x on hive-free, 3,726x on
// deepseek-v4-flash and 2,638x on hive-small. Every one of them carried no
// max_tokens, and capCaptureAtCeiling short circuited to the raw hold whenever
// the caller set no ceiling, so the one bound the capture had did not apply.
//
// The rule these tests pin: a capture is the catalog price of the tokens the
// request actually involved, bounded above by the caller's ceiling when they
// set one and by the hold always, and floored at one credit so no served
// request is ever free (D-034, D-048, D-055).

// minimumCapture is the one-credit floor capCaptureAtCeiling applies when a
// request carries no priceable quantity at all. Several streaming tests use the
// bare `{}` request body, which has no prompt, and exercise shapes that
// accumulate no content, so there is genuinely nothing to price and the floor
// is the whole answer. It is asserted exactly rather than as a range, because a
// range that merely says "at most the hold" is satisfied by charging the hold,
// which is the defect itself.
const minimumCapture = 1

// assertPricedCapture pins what a fail-closed hold capture charges. want is the
// catalog price of the tokens the request involved, or minimumCapture where it
// involved none.
func assertPricedCapture(t *testing.T, call map[string]any, want int64) {
	t.Helper()
	credits := finalizeInt64(t, call, "actual_credits")
	if credits != want {
		t.Errorf("actual_credits = %d, want %d: a capture is the catalog price of the tokens involved, floored at one credit, never the flat authorization hold (#1198)", credits, want)
	}
}

// newAccountingMockWithHold is newAccountingMock with a caller-chosen hold, so
// a test can reproduce the production hold size (DefaultHoldText) rather than
// the 10,000 the shared mock returns. The magnitude is the whole point here:
// at a 10,000 hold the defect is a rounding error, at 100,000,000 it is the
// 355,872x overcharge that was measured.
func newAccountingMockWithHold(rec *accountingRecorder, hold int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/internal/accounting/reservations":
			_ = json.NewEncoder(w).Encode(ReservationResult{
				ID: "res-test-1", AccountID: "acct-test-1", Status: "active", EstimatedCredits: hold,
			})
		case "/internal/usage/attempts":
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "streaming",
			})
		}
	}))
}

// shortAnswerNoUsageSSEServer streams a short, ordinary answer and no usage
// object anywhere: the exact shape behind the seventeen measured rows.
func shortAnswerNoUsageSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		stop := "stop"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "Hello.", &stop))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// TestExecuteStreaming_NoCallerCeiling_CaptureIsPricedNotTheFlatHold is the red
// test for #1198. A six-character answer on hive-free, no usage block, no
// max_tokens, against the production 100,000,000 credit hold. Before the fix
// the charge is that hold in full; after it, it is the catalog price of the
// handful of tokens involved.
func TestExecuteStreaming_NoCallerCeiling_CaptureIsPricedNotTheFlatHold(t *testing.T) {
	litellmSrv := shortAnswerNoUsageSSEServer()
	defer litellmSrv.Close()
	routingSrv := newRoutingMockFreePool(0)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMockWithHold(rec, DefaultHoldText)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	// No max_tokens and no max_completion_tokens: the shape every measured row
	// carried, and the shape Open WebUI and a bare SDK call both produce.
	body := []byte(`{"model":"hive-free","messages":[{"role":"user","content":"Say hello please"}],"stream":true}`)
	w := newHeaderCommitRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, body, "hive-free", "hive-free",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, DefaultHoldText, false, nil, orch.litellm.ChatCompletion)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("executeStreaming did not return in time")
	}

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("content was delivered; expected FinalizeReservation, got calls: %+v", rec.calls)
	}
	credits := finalizeInt64(t, finalize, "actual_credits")

	// The bound this test exists to enforce: the catalog price of the tokens
	// the request actually involved. Computed here from the same helpers the
	// production path uses, so the assertion cannot drift from the pricing.
	promptTokens := estimateCompletionTokens(promptText(EndpointChatCompletions, body))
	completionTokens := estimateCompletionTokens("Hello.")
	want := CreditsForTokens(routeForPricingAssertions, promptTokens, 0, 0, completionTokens)
	if credits > want {
		t.Errorf("capture charged %d credits, more than the %d this request costs at catalog price for %d prompt and %d completion tokens: the flat %d credit hold is an authorization floor, never a measurement (#1198, #636)",
			credits, want, promptTokens, completionTokens, DefaultHoldText)
	}
	if credits < 1 {
		t.Errorf("capture charged %d credits: a served request is never free (D-034, D-055)", credits)
	}
	// The properties #1215 added this capture for, all still required.
	if confirmed, _ := finalize["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must stay false: no real usage arrived, so the charge routes to reconciliation")
	}
	if finalize["status"] != "completed" {
		t.Errorf("status = %v, want completed", finalize["status"])
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a delivered stream must never release its hold")
	}
}

// TestExecuteSync_ZeroContentLength_UpstreamActual_KeepsTheReportedCost covers
// a variable-price alias reaching the zero-content guard.
//
// UpstreamActualSettlement has already produced the charge for such a route:
// the cost the upstream itself reported, times the margin. The zero-content
// branch then overwrote it with capCaptureAtCeiling, whose contract is to
// return its credits argument untouched for an upstream-actual route, and that
// argument is reservation.Held(). So a readable upstream cost was discarded in
// favour of the whole authorization hold, which is the same overcharge shape
// #1198 is about, on the one alias family (hive-auto, openrouter-auto) that has
// no catalog price to fall back on.
func TestExecuteSync_ZeroContentLength_UpstreamActual_KeepsTheReportedCost(t *testing.T) {
	// content null, finish_reason length (so the guard trips), and a usage
	// block carrying both real token counts and the upstream's reported cost.
	const emptyWithCost = `{"id":"gen-upstream-empty","object":"chat.completion","created":1,"model":"route",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"length"}],` +
		`"usage":{"prompt_tokens":76,"completion_tokens":512,"total_tokens":588,"cost":0.0004}}`
	litellm := newScriptedLiteLLM(t, []string{emptyWithCost, emptyWithCost})
	defer litellm.server.Close()
	routing := newRoutingMockUpstreamActual()
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMockWithHold(rec, DefaultHoldText)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"hive-auto","messages":[{"role":"user","content":"Say hello"}]}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "hive-auto",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	want, err := CreditsForUpstreamCost(big.NewRat(4, 10000))
	if err != nil {
		t.Fatalf("CreditsForUpstreamCost: %v", err)
	}
	credits := finalizeInt64(t, finalize, "actual_credits")
	if credits != want {
		t.Errorf("actual_credits = %d, want %d: the upstream reported its own cost for this generation, so the charge is that cost and never the %d credit authorization hold (#1198)",
			credits, want, int64(DefaultHoldText))
	}
}

// TestExecuteSync_ZeroContentNoUsage_NoCeiling_CaptureIsPriced is the
// synchronous twin: the zero-content retry guard fires, the provider reported
// no usage at all, and the caller set no ceiling. Before the fix this is the
// flat hold; after it, the catalog price of the prompt, which is the only
// quantity anything can see.
func TestExecuteSync_ZeroContentNoUsage_NoCeiling_CaptureIsPriced(t *testing.T) {
	// finish_reason length, content null, and NO usage object: the guard trips
	// and settlement has nothing measured to price from.
	emptyNoUsage := `{"id":"chatcmpl-empty","object":"chat.completion","created":1,"model":"route","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"length"}]}`
	litellm := newScriptedLiteLLM(t, []string{emptyNoUsage, emptyNoUsage})
	defer litellm.server.Close()
	routing := newRoutingMockReserving(litellm.server.URL, 4096)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMockWithHold(rec, DefaultHoldText)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello"}]}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	credits := finalizeInt64(t, finalize, "actual_credits")

	promptTokens := estimateCompletionTokens(promptText(EndpointChatCompletions, body))
	routeHiveFast := SelectRouteResult{AliasID: "gpt-4o", Provider: "openrouter", Pricing: catalogHiveFast, PriceUnit: PriceUnitTokens}
	want := CreditsForTokens(routeHiveFast, promptTokens, 0, 0, 0)
	if credits > want {
		t.Errorf("zero-content capture charged %d credits, more than the %d the %d prompt tokens cost at catalog price: the flat %d credit hold is an authorization floor, never a measurement (#1198)",
			credits, want, promptTokens, DefaultHoldText)
	}
	if credits < 1 {
		t.Errorf("zero-content capture charged %d credits: a served request is never free (D-034, D-055)", credits)
	}
	if confirmed, _ := finalize["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must stay false so reconciliation still sees the capture")
	}
}
