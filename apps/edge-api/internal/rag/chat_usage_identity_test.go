package rag

// /v1/rag/chat is the other surface issue #1472's rule has to hold on, and
// both halves of it used to forward whatever total_tokens the upstream
// reported: the synchronous half copied it straight into ChatUsage, and the
// streaming half relayed the sanitized frame containing it.
//
// The synchronous half is covered here. The streaming half's correction is
// inference.EnforceUsageIdentityInFrame, applied in the relay loop and covered
// by TestUsageIdentityInFrame_CorrectsTheTotalAndLeavesEveryOtherMemberAlone
// in that package, since it is one shared function rather than a second copy
// of the rule written here.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// upstreamWithWrongTotal is canned200Response's shape with the live #1472
// disagreement in it: a total six times its own components, at a magnitude
// where the 1-credit floor cannot make a wrong charge look right.
const upstreamWithWrongTotal = `{"id":"upstream-123","choices":[{"message":{"role":"assistant","content":"The answer is 42 [1]."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":93}}`

// TestRAGChatHoldsTheResponseUsageToTheIdentity asserts the number the
// customer receives and the number the ledger is charged on the SAME request,
// because the two failure modes are different and either alone would pass a
// narrower test: a correction that inflated completion_tokens to meet the
// total would satisfy the identity and begin billing a class that has never
// been billed (D-055), and a correction applied only to the charge would leave
// the caller reading a total they were not billed on.
func TestRAGChatHoldsTheResponseUsageToTheIdentity(t *testing.T) {
	const inPrice, outPrice int64 = 300_000, 1_200_000
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		pricedSelectRoute("route-test", inPrice, outPrice),
		fakeDispatch(http.StatusOK, upstreamWithWrongTotal, nil))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
	}, uuid.New()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var resp ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, w.Body.String())
	}
	if resp.Usage == nil {
		t.Fatalf("response carried no usage block; body %s", w.Body.String())
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Errorf("components were rewritten, which would move the charge: %+v", resp.Usage)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15 (prompt 10 + completion 5): the customer received a total that disagrees with its own components",
			resp.Usage.TotalTokens)
	}

	acct.mu.Lock()
	defer acct.mu.Unlock()
	if len(acct.finalized) != 1 {
		t.Fatalf("want exactly one settlement, got %d", len(acct.finalized))
	}
	got := acct.finalized[0]
	want := (10*inPrice + 5*outPrice + 500_000) / 1_000_000
	if got.ActualCredits != want {
		t.Errorf("charged %d credits, want %d: the charge prices the components and an inflated total must not move it", got.ActualCredits, want)
	}
	if !got.TerminalUsageConfirmed {
		t.Error("a provider usage block with real token counts must settle as confirmed")
	}
}

// upstreamAlongsideConvention is the live #1472 shape with its breakdown
// present: 4 + 1 + 26 = 31, the upstream counting reasoning ALONGSIDE
// completion rather than inside it.
const upstreamAlongsideConvention = `{"id":"upstream-123","choices":[{"message":{"role":"assistant","content":"The answer is 42 [1]."},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":31,"completion_tokens_details":{"reasoning_tokens":26}}}`

// TestRAGChatKeepsTheReasoningCountWhenItRestatesTheTotal is the guard for the
// one way this endpoint could still lose a measured quantity.
//
// The restatement replaces the upstream's total of 31 with 5, and the only
// thing that makes that lossless is reasoning_tokens still being in the
// response: a client that could derive 26 by subtraction before must be able
// to read it directly after. ChatUsage carried three fields and no breakdown,
// so this endpoint dropped the 26 on the floor while the other five surfaces
// kept it, which is the outcome the capping revision was rejected for, reached
// by a different mechanism.
//
// It goes red the moment the reasoning count stops surviving into the
// response, whether by removing the field from ChatUsage, by not decoding it
// off the upstream, or by not copying it through the identity call.
func TestRAGChatKeepsTheReasoningCountWhenItRestatesTheTotal(t *testing.T) {
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		pricedSelectRoute("route-test", 300_000, 1_200_000),
		fakeDispatch(http.StatusOK, upstreamAlongsideConvention, nil))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
	}, uuid.New()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var resp ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, w.Body.String())
	}
	if resp.Usage == nil {
		t.Fatalf("response carried no usage block; body %s", w.Body.String())
	}
	if resp.Usage.TotalTokens != 5 || resp.Usage.PromptTokens != 4 || resp.Usage.CompletionTokens != 1 {
		t.Errorf("totals = %+v, want prompt 4, completion 1, total 5", resp.Usage)
	}
	if resp.Usage.CompletionTokensDetails == nil {
		t.Fatalf("the restatement dropped the reasoning breakdown, so the 26 the upstream measured is unrecoverable from this response: %s", w.Body.String())
	}
	if resp.Usage.CompletionTokensDetails.ReasoningTokens != 26 {
		t.Errorf("reasoning_tokens = %d, want the measured 26: without it a client reading total 5 has no way back to the upstream's 31",
			resp.Usage.CompletionTokensDetails.ReasoningTokens)
	}

	// The charge is the second half of the same assertion, on the same
	// request: carrying a breakdown must not begin billing it.
	acct.mu.Lock()
	defer acct.mu.Unlock()
	if len(acct.finalized) != 1 {
		t.Fatalf("want exactly one settlement, got %d", len(acct.finalized))
	}
	if got := acct.finalized[0]; got.OutputTokens != 1 {
		t.Errorf("settled output tokens = %d, want 1: reasoning_tokens is reported, never billed (D-055)", got.OutputTokens)
	}
}

// TestRAGChatReasoningCounterCanFireOnThisEndpoint is the reachability guard,
// and it exists because the previous revision failed exactly this in spirit:
// the alongside branch was unreachable here, so the counter built to measure
// unbilled reasoning could never increment in the population it was built to
// observe, and a clean dashboard would have meant nothing.
//
// A metric that cannot fire where the loss happens is not evidence of safety.
func TestRAGChatReasoningCounterCanFireOnThisEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	inference.NewStageMetrics(reg)
	before := unbilledReasoning(t, reg, "alongside")

	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		pricedSelectRoute("route-test", 300_000, 1_200_000),
		fakeDispatch(http.StatusOK, upstreamAlongsideConvention, nil))
	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
	}, uuid.New()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	if got := unbilledReasoning(t, reg, "alongside"); got != before+26 {
		t.Errorf("unbilled-reasoning counter = %v, want %v: this endpoint must be able to increment the series that measures it, or the series is blind exactly where the quantity is at risk",
			got, before+26)
	}
}

// unbilledReasoning reads one labelled value of
// hive_usage_reasoning_tokens_unbilled_total off a registry.
//
// Gathered rather than reached for directly because the counter is unexported
// in the inference package, and exporting a test hook into production code to
// read a metric is a worse trade than ten lines here. It fails the test if the
// series is missing, which is deliberate: an absent series is the failure this
// pair of tests is about, so it must never be read as a zero.
func unbilledReasoning(t *testing.T, reg *prometheus.Registry, convention string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "hive_usage_reasoning_tokens_unbilled_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "convention" && label.GetValue() == convention {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	t.Fatalf("hive_usage_reasoning_tokens_unbilled_total{convention=%q} is not exposed at all", convention)
	return 0
}
