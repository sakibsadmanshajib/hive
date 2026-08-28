package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #1283: a caller who sets max_tokens: 8 on hive-free received 1650
// completion tokens and was billed for all of them, roughly 200 times the
// ceiling they set to bound their own spend.
//
// The invariant these tests pin, stated once so every case below can be read
// against it: A REQUEST THAT SPECIFIES max_tokens: N MUST NEVER BE BILLED FOR
// MORE THAN N COMPLETION TOKENS. It is enforced at two boundaries and tested
// at both, because either one alone is a hope rather than a proof:
//
//   - request boundary: the caller's ceiling reaches the provider unchanged,
//     so the response they receive is the size they asked for.
//   - settlement boundary: whatever the provider reports, the ledger charge,
//     the usage rollup and the usage block the caller reads are all capped at
//     the caller's own ceiling.

// overrunBody is an ordinary chat completion whose reported completion_tokens
// blows straight past any small caller ceiling: the live shape from #1283.
func overrunBody(promptTokens, completionTokens int) string {
	return fmt.Sprintf(`{"id":"chatcmpl-overrun","object":"chat.completion","created":1,"model":"route","choices":[{"index":0,"message":{"role":"assistant","content":"a very long story about the sea"},"finish_reason":"length"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
		promptTokens, completionTokens, promptTokens+completionTokens)
}

// catalogHiveFree is the live hive-free row: 1,000,000 credits per million
// input tokens and 4,000,000 per million output (D-048, migration
// 20260824_02_free_pool_router.sql). These tests price against the real row so
// the magnitude they assert is the magnitude #1283 measured, not a fixture's.
var catalogHiveFree = FixedPricing(1_000_000, 4_000_000)

// routeForPricingAssertions mirrors what newRoutingMockFreePool answers with,
// so a test can compute the credits a given token count should cost without
// hardcoding the arithmetic of CreditsForTokens.
var routeForPricingAssertions = SelectRouteResult{
	AliasID:   "hive-free",
	Provider:  "groq",
	Pricing:   catalogHiveFree,
	PriceUnit: PriceUnitTokens,
}

// newRoutingMockFreePool stands in for control-plane resolving the hive-free
// alias: the real per-million credit price, and the per-member reasoning
// reserve that arms the #1171 mechanisms.
func newRoutingMockFreePool(reserve int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:                "hive-free",
			RouteID:                "route-free-pool-groq",
			LiteLLMModelName:       "route-free-pool",
			Provider:               "groq",
			Pricing:                catalogHiveFree,
			PriceUnit:              PriceUnitTokens,
			ReasoningReserveTokens: reserve,
		})
	}))
}

// finalizeInt64 pulls a numeric field out of a recorded finalize call.
func finalizeInt64(t *testing.T, call map[string]any, field string) int64 {
	t.Helper()
	raw, ok := call[field]
	if !ok {
		t.Fatalf("finalize call has no %q field; call was %v", field, call)
	}
	f, ok := raw.(float64)
	if !ok {
		t.Fatalf("finalize field %q = %v (%T), want a number", field, raw, raw)
	}
	return int64(f)
}

// TestExecuteSync_CallerCeilingReachesProviderUnchanged is the request-boundary
// half. A pool that reserves reasoning headroom must not raise a ceiling the
// caller set: the reserve was inflating max_tokens: 8 to 4104 upstream, and
// since nothing upstream separates hidden reasoning from visible output, the
// model spent the inflated ceiling on whichever it emitted first.
func TestExecuteSync_CallerCeilingReachesProviderUnchanged(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{contentBody()})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(4096)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story about the sea."}],"max_tokens":8}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	sent := litellm.sentBody(0)
	var probe struct {
		MaxTokens *int `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(sent), &probe); err != nil {
		t.Fatalf("dispatched body is not valid JSON: %v (%s)", err, sent)
	}
	if probe.MaxTokens == nil {
		t.Fatalf("dispatched body dropped max_tokens entirely: %s", sent)
	}
	if *probe.MaxTokens != 8 {
		t.Errorf("dispatched max_tokens = %d, want the caller's own 8: a reserve may not raise a ceiling the caller set (body: %s)", *probe.MaxTokens, sent)
	}
}

// TestExecuteSync_BilledCompletionTokensCappedAtCallerCeiling is the
// settlement-boundary half on the synchronous path, and the money assertion of
// #1283: the provider reports 1650 completion tokens against a ceiling of 8,
// and the charge, the rollup counts and the caller-visible usage block must all
// read 8.
func TestExecuteSync_BilledCompletionTokensCappedAtCallerCeiling(t *testing.T) {
	const (
		promptTokens = 20
		reported     = 1650
		ceiling      = 8
	)
	litellm := newScriptedLiteLLM(t, []string{overrunBody(promptTokens, reported)})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(0)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story about the sea."}],"max_tokens":8}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	if got := finalizeInt64(t, finalize, "output_tokens"); got != ceiling {
		t.Errorf("finalize output_tokens = %d, want %d: a request capped at %d completion tokens may not meter more", got, ceiling, ceiling)
	}
	wantCredits := CreditsForTokens(routeForPricingAssertions, promptTokens, 0, 0, ceiling)
	unclampedCredits := CreditsForTokens(routeForPricingAssertions, promptTokens, 0, 0, reported)
	if got := finalizeInt64(t, finalize, "actual_credits"); got != wantCredits {
		t.Errorf("actual_credits = %d, want %d (the price of %d completion tokens); the unclamped charge would be %d",
			got, wantCredits, ceiling, unclampedCredits)
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp.Usage == nil {
		t.Fatalf("response carries no usage block: %s", w.Body.String())
	}
	if resp.Usage.CompletionTokens != ceiling {
		t.Errorf("response usage.completion_tokens = %d, want %d: the number the caller reads must be the number they are billed for",
			resp.Usage.CompletionTokens, ceiling)
	}
	if resp.Usage.TotalTokens != promptTokens+ceiling {
		t.Errorf("response usage.total_tokens = %d, want %d", resp.Usage.TotalTokens, promptTokens+ceiling)
	}
}

// TestExecuteSync_NoCeilingLeavesUsageAlone is the other side of the guard: a
// caller who declared no budget gets exactly what the provider reported, so the
// clamp cannot quietly become a discount on every request.
func TestExecuteSync_NoCeilingLeavesUsageAlone(t *testing.T) {
	const reported = 1650
	litellm := newScriptedLiteLLM(t, []string{overrunBody(20, reported)})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(0)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story about the sea."}]}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	if got := finalizeInt64(t, finalize, "output_tokens"); got != reported {
		t.Errorf("finalize output_tokens = %d, want the reported %d: no ceiling was set, so there is nothing to clamp to", got, reported)
	}
}

// TestExecuteSync_ZeroContentCaptureBoundedByCallerCeiling closes the branch
// the other half of #1171 opens. An empty-content completion captures the
// reservation hold rather than settling full price, and the hold is a flat
// $0.10-equivalent authorization floor: capturing it for a request capped at 8
// completion tokens is a far larger breach of the same invariant than the one
// #1283 reported. The capture stays fail-closed (never zero) but may not exceed
// what the caller's own ceiling could have cost.
func TestExecuteSync_ZeroContentCaptureBoundedByCallerCeiling(t *testing.T) {
	const (
		promptTokens = 76
		ceiling      = 8
	)
	litellm := newScriptedLiteLLM(t, []string{emptyLengthBody(promptTokens), emptyLengthBody(promptTokens)})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(4096)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello"}],"max_tokens":8}`)
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
	ceilingCredits := CreditsForTokens(routeForPricingAssertions, promptTokens, 0, 0, ceiling)
	if credits > ceilingCredits {
		t.Errorf("zero-content capture charged %d credits against a ceiling worth %d (hold is %d): a capture may not bill past the caller's own ceiling",
			credits, ceilingCredits, DefaultHoldText)
	}
	if credits < 1 {
		t.Errorf("zero-content capture charged %d credits: the money path fails closed and never bills zero for a served request (D-034)", credits)
	}
	if confirmed, _ := finalize["terminal_usage_confirmed"].(bool); confirmed {
		t.Errorf("terminal_usage_confirmed = true on a zero-content capture; the figure is not measured truth")
	}
}

// sseOverrunServer streams one content chunk and one terminal usage frame whose
// completion_tokens sits far above any small caller ceiling.
func sseOverrunServer(promptTokens, completionTokens int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"route\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a very long story about the sea\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"route\",\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n",
			promptTokens, completionTokens, promptTokens+completionTokens)
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// TestExecuteStreaming_BilledCompletionTokensCappedAtCallerCeiling is the same
// money assertion on the streaming path. Streaming is where agent traffic and
// the chat surface actually live, so a clamp that held only on the synchronous
// path would leave the defect in place for most real requests.
func TestExecuteStreaming_BilledCompletionTokensCappedAtCallerCeiling(t *testing.T) {
	const (
		promptTokens = 20
		reported     = 1650
		ceiling      = 8
	)
	litellmSrv := sseOverrunServer(promptTokens, reported)
	defer litellmSrv.Close()
	routingSrv := newRoutingMockFreePool(0)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story about the sea."}],"max_tokens":8,"stream":true,"stream_options":{"include_usage":true}}`)
	w := newHeaderCommitRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, DefaultHoldText, true, nil, orch.litellm.ChatCompletion)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executeStreaming did not return in time")
	}

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	if got := finalizeInt64(t, finalize, "output_tokens"); got != ceiling {
		t.Errorf("finalize output_tokens = %d, want %d", got, ceiling)
	}
	wantCredits := CreditsForTokens(routeForPricingAssertions, promptTokens, 0, 0, ceiling)
	if got := finalizeInt64(t, finalize, "actual_credits"); got != wantCredits {
		t.Errorf("actual_credits = %d, want %d (the price of %d completion tokens); unclamped would be %d",
			got, wantCredits, ceiling, CreditsForTokens(routeForPricingAssertions, promptTokens, 0, 0, reported))
	}

	// The caller asked for the usage frame, so the one they receive must carry
	// the same figure the ledger recorded.
	if body := w.Body.String(); !strings.Contains(body, `"completion_tokens":8`) {
		t.Errorf("forwarded usage frame does not carry the clamped completion_tokens; stream was:\n%s", body)
	}
}

// --- unit tests over the ceiling reader ---

func TestRequestedCompletionCeiling(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		body     string
		want     int64
	}{
		{"chat max_tokens", EndpointChatCompletions, `{"max_tokens":8}`, 8},
		{"chat max_completion_tokens", EndpointChatCompletions, `{"max_completion_tokens":16}`, 16},
		{
			// A self-contradictory request still gets the guarantee its
			// smaller ceiling states, which is the customer-favouring reading
			// and the one the stated invariant demands literally.
			"chat both spellings takes the smaller", EndpointChatCompletions,
			`{"max_tokens":8,"max_completion_tokens":100}`, 8,
		},
		{"chat none set", EndpointChatCompletions, `{"model":"m"}`, 0},
		{"chat null ceiling", EndpointChatCompletions, `{"max_tokens":null}`, 0},
		{"chat zero ceiling", EndpointChatCompletions, `{"max_tokens":0}`, 0},
		{"chat negative ceiling", EndpointChatCompletions, `{"max_tokens":-5}`, 0},
		{"chat unparseable ceiling", EndpointChatCompletions, `{"max_tokens":"lots"}`, 0},
		{"legacy completions", EndpointCompletions, `{"max_tokens":32}`, 32},
		{
			// max_completion_tokens is not a legacy-completions field, so it
			// must not be read as a ceiling there.
			"legacy completions ignores chat spelling", EndpointCompletions,
			`{"max_completion_tokens":32}`, 0,
		},
		{"responses", EndpointResponses, `{"max_output_tokens":64}`, 64},
		{"embeddings has no ceiling", EndpointEmbeddings, `{"max_tokens":8}`, 0},
		{"malformed body", EndpointChatCompletions, `not json`, 0},
		{"empty body", EndpointChatCompletions, ``, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestedCompletionCeiling(tc.endpoint, []byte(tc.body)); got != tc.want {
				t.Errorf("requestedCompletionCeiling(%s, %s) = %d, want %d", tc.endpoint, tc.body, got, tc.want)
			}
		})
	}
}

func TestClampUsageToCeiling(t *testing.T) {
	t.Run("caps and recomputes the total", func(t *testing.T) {
		usage := &UsageResponse{PromptTokens: 20, CompletionTokens: 1650, TotalTokens: 1670}
		if !clampUsageToCeiling(usage, routeForPricingAssertions, 8, EndpointChatCompletions, "hive-free") {
			t.Fatal("clampUsageToCeiling reported no change on an over-ceiling usage block")
		}
		if usage.CompletionTokens != 8 || usage.TotalTokens != 28 {
			t.Errorf("usage = %+v, want completion 8 and total 28", usage)
		}
	})
	t.Run("leaves an in-budget response alone", func(t *testing.T) {
		usage := &UsageResponse{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25}
		if clampUsageToCeiling(usage, routeForPricingAssertions, 8, EndpointChatCompletions, "hive-free") {
			t.Error("clamped a usage block already inside the ceiling")
		}
		if usage.CompletionTokens != 5 || usage.TotalTokens != 25 {
			t.Errorf("usage = %+v, want it untouched", usage)
		}
	})
	t.Run("no ceiling and nil usage are pass-throughs", func(t *testing.T) {
		usage := &UsageResponse{PromptTokens: 20, CompletionTokens: 1650, TotalTokens: 1670}
		if clampUsageToCeiling(usage, routeForPricingAssertions, 0, EndpointChatCompletions, "hive-free") {
			t.Error("clamped with no ceiling set")
		}
		if clampUsageToCeiling(nil, routeForPricingAssertions, 8, EndpointChatCompletions, "hive-free") {
			t.Error("clamped a nil usage block")
		}
	})
	// Review finding 3 on PR #1305: a variable-price alias settles on the cost
	// the upstream reported, which this clamp never touches, so clamping the
	// usage block there would only advertise a completion count nobody was
	// billed on.
	t.Run("a variable-price alias is left alone", func(t *testing.T) {
		route := SelectRouteResult{AliasID: "hive-auto", Pricing: UpstreamActualPricing(DefaultHoldText), PriceUnit: PriceUnitTokens}
		usage := &UsageResponse{PromptTokens: 20, CompletionTokens: 1650, TotalTokens: 1670}
		if clampUsageToCeiling(usage, route, 8, EndpointChatCompletions, "hive-auto") {
			t.Error("clamped the usage block of an alias billed on the upstream reported cost")
		}
		if usage.CompletionTokens != 1650 || usage.TotalTokens != 1670 {
			t.Errorf("usage = %+v, want it untouched", usage)
		}
	})
}

// TestSyncOverrunDispatchesOnce guards the guard: the clamp must not turn an
// ordinary over-ceiling response into a retry or a second provider call.
func TestSyncOverrunDispatchesOnce(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{overrunBody(20, 1650)})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(0)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	if got := atomic.LoadInt64(&litellm.hits); got != 1 {
		t.Errorf("provider dispatched %d time(s), want 1", got)
	}
}

// sseNoUsageServer streams content and ends without ever sending a usage frame,
// the shape settleStream captures the reservation hold for (#1215).
func sseNoUsageServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"route\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a very long story about the sea\"},\"finish_reason\":\"length\"}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// TestExecuteStreaming_HoldCaptureBoundedByCallerCeiling is the streaming twin
// of the zero-content capture bound. A stream that delivers output but no usage
// block settles at the reservation hold, a flat authorization floor; against a
// request capped at 8 completion tokens that is a far larger breach of the same
// invariant than the overrun #1283 reported.
func TestExecuteStreaming_HoldCaptureBoundedByCallerCeiling(t *testing.T) {
	const ceiling = 8
	litellmSrv := sseNoUsageServer()
	defer litellmSrv.Close()
	routingSrv := newRoutingMockFreePool(0)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story about the sea."}],"max_tokens":8,"stream":true}`)
	w := newHeaderCommitRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, DefaultHoldText, false, nil, orch.litellm.ChatCompletion)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executeStreaming did not return in time")
	}

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	credits := finalizeInt64(t, finalize, "actual_credits")
	// acc.FreshInputTokens is 0 here, since no usage block ever arrived, which
	// is the whole point of this shape. The ceiling price is therefore the
	// output side plus an ESTIMATED prompt, not the output side alone: pricing
	// the prompt at zero because nobody counted it gives the expensive half of
	// the request away (review round two, finding 3, and
	// TestExecuteStreaming_HoldCaptureKeepsThePromptCost below).
	promptEstimate := estimateCompletionTokens(promptText(EndpointChatCompletions, body))
	ceilingCredits := CreditsForTokens(routeForPricingAssertions, promptEstimate, 0, 0, ceiling)
	if credits > ceilingCredits {
		t.Errorf("hold capture charged %d credits against a ceiling worth %d: a capture may not bill past the caller's own ceiling",
			credits, ceilingCredits)
	}
	if credits < 1 {
		t.Errorf("hold capture charged %d credits: a served stream never bills zero (D-034)", credits)
	}
}
