package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Review findings on PR #1305 (issue #1283). Each test here is the
// reproduction the reviewer measured, kept as the regression guard for the fix
// that answers it. They reuse the harness in max_tokens_enforcement_test.go
// and zero_content_guard_test.go rather than growing a second one.

// contentOnlyBody is a 200 chat completion carrying visible content and NO
// usage block: the branch settlement has to price from content length, which
// is where an unbounded estimate can outrun the ceiling the caller set.
func contentOnlyBody(content string) string {
	escaped, _ := json.Marshal(content)
	return `{"id":"chatcmpl-nousage","object":"chat.completion","created":1,` +
		`"model":"route","choices":[{"index":0,"message":{"role":"assistant",` +
		`"content":` + string(escaped) + `},"finish_reason":"stop"}]}`
}

// upstreamActualBody is a 200 chat completion whose usage block carries the
// cost the upstream reported, the figure a variable-price alias settles on.
func upstreamActualBody(promptTokens, completionTokens int, costUSD string) string {
	return fmt.Sprintf(`{"id":"gen-upstream-1","object":"chat.completion",`+
		`"created":1,"model":"route","choices":[{"index":0,"message":`+
		`{"role":"assistant","content":"a long story"},`+
		`"finish_reason":"length"}],"usage":{"prompt_tokens":%d,`+
		`"completion_tokens":%d,"total_tokens":%d,"cost":%s}}`,
		promptTokens, completionTokens, promptTokens+completionTokens, costUSD)
}

// newRoutingMockUpstreamActual resolves an alias priced on the cost the
// upstream reports, which is what hive-auto is live today (migration
// 20260824_02_free_pool_router).
func newRoutingMockUpstreamActual() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:          "hive-auto",
			RouteID:          "route-auto-1",
			LiteLLMModelName: "route-auto",
			Provider:         "openrouter",
			Pricing:          UpstreamActualPricing(DefaultHoldText),
			PriceUnit:        PriceUnitTokens,
		})
	}))
}

// dispatchedCeilings decodes both chat ceiling spellings out of a dispatched
// body, so a test asserts on what the PROVIDER received rather than on what
// the caller sent.
func dispatchedCeilings(t *testing.T, sent string) (maxTokens, maxCompletionTokens *int) {
	t.Helper()
	var probe struct {
		MaxTokens           *int `json:"max_tokens"`
		MaxCompletionTokens *int `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal([]byte(sent), &probe); err != nil {
		t.Fatalf("dispatched body is not valid JSON: %v (%s)", err, sent)
	}
	return probe.MaxTokens, probe.MaxCompletionTokens
}

// TestExecuteSync_ContradictoryCeilingsPinnedToTheMinimum is review finding 1,
// a caller-triggerable revenue bypass. Settlement holds the request to
// min(max_tokens, max_completion_tokens), but the outbound body forwarded both
// fields verbatim. OpenAI treats max_completion_tokens as authoritative and
// max_tokens as deprecated, and Groq documents the same preference, so a
// request pairing max_tokens 1 with max_completion_tokens 100000 bought a
// full-size generation for the price of one completion token. Both boundaries
// have to enforce the same number, so the minimum is written back over every
// ceiling field present before dispatch.
func TestExecuteSync_ContradictoryCeilingsPinnedToTheMinimum(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{contentBody()})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(0)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story about the sea."}],"max_tokens":1,"max_completion_tokens":100000}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	sent := litellm.sentBody(0)
	maxTokens, maxCompletion := dispatchedCeilings(t, sent)
	if maxTokens == nil || *maxTokens != 1 {
		t.Errorf("dispatched max_tokens = %v, want the 1 the caller set (body: %s)", maxTokens, sent)
	}
	if maxCompletion == nil || *maxCompletion != 1 {
		t.Errorf("dispatched max_completion_tokens = %v, want 1: settlement holds this request to the smaller of the two ceilings, so the provider has to receive that same number or the caller buys a full-size generation for one token (body: %s)", maxCompletion, sent)
	}
}

// TestExecuteStreaming_ContradictoryCeilingsPinnedToTheMinimum is the same
// bypass on the streaming path, which shares the rewrite.
func TestExecuteStreaming_ContradictoryCeilingsPinnedToTheMinimum(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{"data: [DONE]\n\n"})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(0)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story."}],"stream":true,"max_tokens":2,"max_completion_tokens":50000}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")
	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, DefaultHoldText, false, nil, orch.litellm.ChatCompletion)

	sent := litellm.sentBody(0)
	maxTokens, maxCompletion := dispatchedCeilings(t, sent)
	if maxTokens == nil || *maxTokens != 2 {
		t.Errorf("dispatched max_tokens = %v, want the 2 the caller set (body: %s)", maxTokens, sent)
	}
	if maxCompletion == nil || *maxCompletion != 2 {
		t.Errorf("dispatched max_completion_tokens = %v, want 2 (body: %s)", maxCompletion, sent)
	}
}

// TestExecuteSync_ContentEstimateBoundedByCallerCeiling is review finding 2.
// clampUsageToCeiling is a no-op when usage is nil, which is exactly the
// branch that then prices the request by content length, so a 200 carrying
// content and no usage block charged far past the ceiling that was set. The
// streaming path survives this only by accident: its unconfirmed branch
// discards the estimate for a hold capture that IS bounded. The synchronous
// path has no such override, so the estimate itself has to be bounded.
func TestExecuteSync_ContentEstimateBoundedByCallerCeiling(t *testing.T) {
	const ceiling = 8
	longContent := strings.Repeat("a very long story about the sea. ", 200)
	litellm := newScriptedLiteLLM(t, []string{contentOnlyBody(longContent)})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(0)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story about the sea."}],"max_tokens":8}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	call, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	got := finalizeInt64(t, call, "actual_credits")
	promptEstimate := estimateCompletionTokens(promptText(EndpointChatCompletions, body))
	want := CreditsForTokens(routeForPricingAssertions, promptEstimate, 0, 0, ceiling)
	if got != want {
		t.Errorf("actual_credits = %d, want %d: an estimate priced from content length is still bounded by the ceiling of %d completion tokens the caller set", got, want, ceiling)
	}
}

// TestExecuteSync_UpstreamActualUsageIsNotClamped is review finding 3. On a
// variable-price alias the charge comes from the cost the upstream reported
// for the generation, which the settlement clamp never touches. Clamping the
// usage block anyway left the caller reading a completion count they were not
// billed on, which is precisely the divergence clampUsageToCeiling exists to
// prevent: one number, one place. hive-auto is live in this mode.
func TestExecuteSync_UpstreamActualUsageIsNotClamped(t *testing.T) {
	const reported = 1650
	litellm := newScriptedLiteLLM(t, []string{upstreamActualBody(20, reported, "0.0042")})
	defer litellm.server.Close()
	routing := newRoutingMockUpstreamActual()
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"hive-auto","messages":[{"role":"user","content":"Write a long story about the sea."}],"max_tokens":8}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "hive-auto",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Usage *UsageResponse `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, w.Body.String())
	}
	if resp.Usage == nil {
		t.Fatalf("response carries no usage block: %s", w.Body.String())
	}
	if resp.Usage.CompletionTokens != reported {
		t.Errorf("completion_tokens = %d, want the %d the upstream reported: this alias is billed on the cost the upstream reported for the generation rather than on a capped token count, so a clamped usage block advertises a number nobody was charged on", resp.Usage.CompletionTokens, reported)
	}
}

// TestBestOfOtherThanOneRejected is review finding 5. best_of is the same
// unfixed defect n was: nothing in edge-api reads or refuses it, the outbound
// body is re-marshalled from a map so it reaches the provider intact, and the
// generated OpenAPI contract advertises it. It generates best_of completions
// upstream and returns one, so it costs money in both directions against a
// single per-request ceiling.
func TestBestOfOtherThanOneRejected(t *testing.T) {
	h := NewHandler(&Orchestrator{})

	cases := []struct {
		name string
		body string
	}{
		{"best_of=4", `{"model":"gpt-4o","prompt":"Say hello.","max_tokens":16,"best_of":4}`},
		{"best_of=0", `{"model":"gpt-4o","prompt":"hi","best_of":0}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			errObj := decodeErrorBody(t, w)
			if code, _ := errObj["code"].(string); code != "unsupported_parameter" {
				t.Errorf("code = %q, want unsupported_parameter", code)
			}
			if param, _ := errObj["param"].(string); param != "best_of" {
				t.Errorf("param = %v, want best_of: an SDK caller needs to know which field to drop", errObj["param"])
			}
			msg, _ := errObj["message"].(string)
			for _, forbidden := range []string{"OpenAI", "openai", "LiteLLM", "litellm", "openrouter", "OpenRouter", "groq", "Groq"} {
				if strings.Contains(msg, forbidden) {
					t.Errorf("error message leaks provider identity %q: %s", forbidden, msg)
				}
			}
		})
	}
}

// TestBestOfOneOrAbsentPassesThrough keeps the refusal narrow, the same way
// TestNOneOrAbsentPassesThrough does for n. A bare Orchestrator has no
// authorizer, so a request that clears validation ends at 401, which is proof
// it was never refused as a bad request.
func TestBestOfOneOrAbsentPassesThrough(t *testing.T) {
	h := NewHandler(&Orchestrator{})

	for _, tc := range []struct{ name, body string }{
		{"best_of=1", `{"model":"gpt-4o","prompt":"hi","best_of":1}`},
		{"best_of absent", `{"model":"gpt-4o","prompt":"hi"}`},
		{"best_of null", `{"model":"gpt-4o","prompt":"hi","best_of":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code == http.StatusBadRequest {
				t.Fatalf("status = 400 on a servable request: %s", w.Body.String())
			}
		})
	}
}

// Review round two, finding 1. A ceiling field the gateway cannot read as an
// integer was left in the outbound body, so pairing max_tokens 1 with a float
// max_completion_tokens reopened the whole bypass through a spelling the JSON
// number type happens not to cover: the provider parses the float, prefers it,
// and generates against it, while settlement meters 1.
func TestExecuteSync_UnreadableCeilingFieldIsPinnedToo(t *testing.T) {
	litellm := newScriptedLiteLLM(t, []string{contentBody()})
	defer litellm.server.Close()
	routing := newRoutingMockFreePool(0)
	defer routing.Close()
	rec := &accountingRecorder{}
	acct := newAccountingMock(rec)
	defer acct.Close()

	orch := newAuthorizedOrchestrator(acct.URL, routing.URL, litellm.server.URL)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Write a long story."}],"max_tokens":1,"max_completion_tokens":100000.5}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, DefaultHoldText, orch.litellm.ChatCompletion, normalizeChatCompletion)

	sent := litellm.sentBody(0)
	maxTokens, maxCompletion := dispatchedCeilings(t, sent)
	if maxTokens == nil || *maxTokens != 1 {
		t.Errorf("dispatched max_tokens = %v, want 1 (body: %s)", maxTokens, sent)
	}
	if maxCompletion == nil || *maxCompletion != 1 {
		t.Errorf("dispatched max_completion_tokens = %v, want 1: a ceiling this gateway cannot parse is not one it can call smaller, and the provider may well parse it (body: %s)", maxCompletion, sent)
	}
}

// Review round two, finding 2. clampCompletionLimit fills in every ceiling
// field the endpoint speaks, and used to fill them at
// VariablePriceMaxCompletionTokens regardless of a smaller ceiling already in
// the body. On chat that wrote max_completion_tokens 16384 in beside a
// max_tokens of 1, which the provider prefers, so a variable-price alias
// generated four orders of magnitude more than the caller asked for and billed
// the upstream cost of it, with no settlement clamp to fall back on.
func TestEnforceVariablePriceBounds_DoesNotRaiseACeilingTheCallerSet(t *testing.T) {
	route := SelectRouteResult{Pricing: UpstreamActualPricing(200_000), PriceUnit: PriceUnitTokens}
	w := httptest.NewRecorder()
	got, ok := EnforceVariablePriceBounds(w, route, EndpointChatCompletions, "hive-auto",
		[]byte(`{"model":"hive-auto","max_tokens":1}`))
	if !ok {
		t.Fatalf("unexpected refusal: %s", w.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("bounded body is not valid JSON: %v", err)
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		value, present := decoded[field]
		if !present {
			t.Fatalf("%s missing from the bounded body: %s", field, got)
		}
		if number, _ := value.(float64); int64(number) != 1 {
			t.Errorf("%s = %v, want 1: a field this gateway fills in may never carry a larger number than one the caller wrote (body: %s)", field, value, got)
		}
	}
}

// Review round two, finding 3. Both hold-capture branches price the bound with
// capCaptureAtCeiling, whose bound is the catalog price of the WHOLE request at
// the ceiling. Both are reached precisely because no usable usage block
// arrived, so the metered input count is 0, and passing that through priced a
// large prompt at nothing: the capture collapsed to the price of a handful of
// output tokens and served the expensive half of the request for free.
func TestExecuteStreaming_HoldCaptureKeepsThePromptCost(t *testing.T) {
	const ceiling = 8
	litellmSrv := sseNoUsageServer()
	defer litellmSrv.Close()
	routingSrv := newRoutingMockFreePool(0)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	prompt := strings.Repeat("a very long prompt about the sea. ", 4000)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"` + prompt + `"}],"max_tokens":8,"stream":true}`)
	w := newHeaderCommitRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, body, "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, DefaultHoldText, false, nil, orch.litellm.ChatCompletion)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("executeStreaming did not return in time")
	}

	finalize, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("no finalize call recorded; calls: %v", rec.calls)
	}
	credits := finalizeInt64(t, finalize, "actual_credits")
	promptEstimate := estimateCompletionTokens(promptText(EndpointChatCompletions, body))
	outputOnly := CreditsForTokens(routeForPricingAssertions, 0, 0, 0, ceiling)
	want := CreditsForTokens(routeForPricingAssertions, promptEstimate, 0, 0, ceiling)
	if credits <= outputOnly {
		t.Errorf("hold capture charged %d credits, no more than the %d a bare %d-token completion costs: a prompt of roughly %d tokens was served for nothing",
			credits, outputOnly, ceiling, promptEstimate)
	}
	// The capture is the SMALLER of the hold and that bound, so the assertion
	// is one-sided: it may not exceed the ceiling price, and the accounting
	// mock holds less than it here, which is why this is not an equality.
	if credits > want {
		t.Errorf("hold capture charged %d credits, more than the %d this request costs at its ceiling with the prompt priced", credits, want)
	}
	if credits < 1 {
		t.Errorf("hold capture charged %d credits: a served stream never bills zero (D-034)", credits)
	}
}
