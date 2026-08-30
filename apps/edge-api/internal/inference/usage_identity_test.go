package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// --- issue #1472: total_tokens must equal prompt_tokens plus completion_tokens ---
//
// The OpenAI wire contract makes total_tokens a derived figure, and every
// OpenAI-compatible client assumes it. Live integration run 33251802900
// (2026-08-29) saw total_tokens=31 against prompt_tokens+completion_tokens=5
// on the hive-free pool, whose members include a thinking-capable Gemini
// route: Google reports totalTokenCount inclusive of thoughtsTokenCount while
// candidatesTokenCount excludes it, so the two numbers disagree at the wire.
//
// Hive's own adapters never computed that 31. They passed it through, because
// clampZeroCompletionUsage only recomputed the total on the one shape where
// completion_tokens was zero. These tests pin the general rule instead, and
// the scope of that rule is stated rather than implied, because the first
// version of this comment claimed the whole gateway and meant four endpoints.
//
// Covered here and by the sibling tests in apps/edge-api/internal/chat and
// apps/edge-api/internal/rag: /v1/chat/completions, /v1/completions,
// /v1/responses and /v1/embeddings at the clamp boundary every normalizer
// calls; session chat, which serves the Open WebUI front end; and both halves
// of /v1/rag/chat. The correction is keyed on the numbers themselves rather
// than on any provider or model family.
//
// NOT covered, and deliberately: an upstream frame the sanitizer cannot parse
// is dropped rather than corrected, since its contents are unknown; and the
// /v1/batches output lines, which decode the raw LiteLLM body in
// apps/control-plane/internal/batchstore and never cross this package. That
// path also PRICES total_tokens
// (DefaultCreditPolicy.Credits in batchstore/executor/dispatcher.go), which
// makes the same shape an overcharge there rather than a reporting defect,
// and it is corrected in issue #1473 rather than here.
//
// What is NOT changed, and is asserted here so a future change has to argue
// with a test: the CHARGE. Settlement prices the components (prompt and
// completion, split into cache classes), never the total, so correcting the
// total moves no money. Folding the unaccounted tokens into completion_tokens
// instead WOULD move money, by starting to bill a class that was never billed
// before, which D-055 forbids without an owner ruling.

// usageIdentityLogPrefix is the operator-side signal a discrepancy leaves
// behind. A silent correction would hide a provider changing its accounting
// under us, which is the failure this issue is a symptom of.
const usageIdentityLogPrefix = "inference: usage identity violated"

// usageJSONServerWithTotal answers the non-streaming path with a completion
// whose usage block reports an ARBITRARY total, decoupled from its own
// components. usageJSONServer cannot express this: it derives the total from
// the two components, which is exactly the shape that never fails.
func usageJSONServerWithTotal(promptTokens, completionTokens, totalTokens int64, content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		text := content
		stop := "stop"
		_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:     "chatcmpl-test-1",
			Object: "chat.completion",
			Model:  "route",
			Choices: []ChatCompletionChoice{{
				Index:        0,
				Message:      ChatCompletionMessage{Role: "assistant", Content: &text},
				FinishReason: &stop,
			}},
			Usage: &UsageResponse{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      totalTokens,
			},
		})
	}))
}

// syncResponseUsage decodes the usage block the caller actually received.
func syncResponseUsage(t *testing.T, w *httptest.ResponseRecorder) *UsageResponse {
	t.Helper()
	var resp ChatCompletionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response body: %v (body %q)", err, w.Body.String())
	}
	if resp.Usage == nil {
		t.Fatalf("response carried no usage block at all; body %q", w.Body.String())
	}
	return resp.Usage
}

// --- the five required shapes, at the clamp boundary every normalizer calls ---

// TestUsageIdentity_HoldingIdentityPassesThroughUntouched is the no-op case: a
// usage object that already satisfies the identity is not rewritten, and
// leaves NO discrepancy log line.
//
// The absence assertion has a reason: this line is the operator's alarm for a
// provider changing its accounting, and a line on every well-formed request
// would bury the real one under one entry per request. An always-on log is an
// alarm nobody reads.
func TestUsageIdentity_HoldingIdentityPassesThroughUntouched(t *testing.T) {
	u := &UsageResponse{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25}
	logs := captureLogs(t, func() {
		clampZeroCompletionUsage(u, []string{"hello there"}, "id", "alias", EndpointChatCompletions)
	})
	if u.PromptTokens != 20 || u.CompletionTokens != 5 || u.TotalTokens != 25 {
		t.Errorf("a consistent usage object was rewritten: %+v", u)
	}
	if strings.Contains(logs, usageIdentityLogPrefix) {
		t.Errorf("a consistent usage object logged a discrepancy, which buries the real ones: %q", logs)
	}
}

// TestUsageIdentity_TotalAboveComponentsIsCorrectedAndLogged pins the exact
// live shape from issue #1472: total 31 against components summing to 5.
func TestUsageIdentity_TotalAboveComponentsIsCorrectedAndLogged(t *testing.T) {
	u := &UsageResponse{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 31}
	logs := captureLogs(t, func() {
		clampZeroCompletionUsage(u, []string{"Hello!"}, "gen-abc", "hive-free", EndpointChatCompletions)
	})
	if u.TotalTokens != 5 {
		t.Errorf("total_tokens = %d, want 5 (prompt 4 + completion 1)", u.TotalTokens)
	}
	if u.PromptTokens != 4 || u.CompletionTokens != 1 {
		t.Errorf("components were rewritten, which would move the charge: %+v", u)
	}
	if !strings.Contains(logs, usageIdentityLogPrefix) {
		t.Fatalf("a corrected discrepancy must be loud operator-side; logs: %q", logs)
	}
	for _, want := range []string{"alias=hive-free", "upstream_total_tokens=31", "corrected_total_tokens=5", "unaccounted_tokens=26"} {
		if !strings.Contains(logs, want) {
			t.Errorf("discrepancy log missing %q; logs: %q", want, logs)
		}
	}
}

// TestUsageIdentity_TotalBelowComponentsIsCorrectedAndLogged is the other
// direction: an upstream under-reporting its own total.
func TestUsageIdentity_TotalBelowComponentsIsCorrectedAndLogged(t *testing.T) {
	u := &UsageResponse{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 9}
	logs := captureLogs(t, func() {
		clampZeroCompletionUsage(u, []string{"hello there"}, "id", "alias", EndpointChatCompletions)
	})
	if u.TotalTokens != 25 {
		t.Errorf("total_tokens = %d, want 25", u.TotalTokens)
	}
	if !strings.Contains(logs, "unaccounted_tokens=-16") {
		t.Errorf("expected the signed shortfall in the log; logs: %q", logs)
	}
}

// TestUsageIdentity_ZeroCompletionClampStillHolds is the regression guard for
// the behaviour that already existed: completion_tokens=0 on a non-empty
// response is estimated from the output text, and the total follows it.
func TestUsageIdentity_ZeroCompletionClampStillHolds(t *testing.T) {
	u := &UsageResponse{PromptTokens: 4, CompletionTokens: 0, TotalTokens: 4}
	logs := captureLogs(t, func() {
		clampZeroCompletionUsage(u, []string{"hello there, this is generated output"}, "id", "alias", EndpointChatCompletions)
	})
	if u.CompletionTokens == 0 {
		t.Fatalf("completion_tokens was not clamped against real output text: %+v", u)
	}
	if u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		t.Errorf("identity broken after the zero-completion clamp: %d != %d + %d",
			u.TotalTokens, u.PromptTokens, u.CompletionTokens)
	}
	if !strings.Contains(logs, "usage clamp engaged") {
		t.Errorf("the zero-completion clamp must stay loud; logs: %q", logs)
	}
}

// TestUsageIdentity_TotalAbsentIsFilledFromComponents covers a frame with no
// total at all. Go decodes an absent total_tokens to 0, so the absent case and
// a reported zero are the same value here; both are wrong against non-zero
// components and both are corrected.
func TestUsageIdentity_TotalAbsentIsFilledFromComponents(t *testing.T) {
	var u UsageResponse
	if err := json.Unmarshal([]byte(`{"prompt_tokens":7,"completion_tokens":3}`), &u); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	logs := captureLogs(t, func() {
		clampZeroCompletionUsage(&u, []string{"hi"}, "id", "alias", EndpointChatCompletions)
	})
	if u.TotalTokens != 10 {
		t.Errorf("total_tokens = %d, want 10", u.TotalTokens)
	}
	if !strings.Contains(logs, usageIdentityLogPrefix) {
		t.Errorf("a missing total is a discrepancy and must be logged; logs: %q", logs)
	}
}

// TestUsageIdentity_EmbeddingsTotalFollowsPromptTokens covers the one endpoint
// whose usage object has no completion side at all: for embeddings the
// identity reduces to total_tokens == prompt_tokens.
func TestUsageIdentity_EmbeddingsTotalFollowsPromptTokens(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"route","usage":{"prompt_tokens":12,"total_tokens":97}}`)
	var normalized []byte
	var usage *UsageResponse
	logs := captureLogs(t, func() {
		var err error
		normalized, usage, err = normalizeEmbeddings(body, "hive-embedding-default")
		if err != nil {
			t.Fatalf("normalizeEmbeddings: %v", err)
		}
	})
	if usage == nil || usage.TotalTokens != 12 {
		t.Errorf("accounting usage total = %+v, want total_tokens 12", usage)
	}
	var resp EmbeddingsResponse
	if err := json.Unmarshal(normalized, &resp); err != nil {
		t.Fatalf("decode normalized embeddings: %v", err)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 12 {
		t.Errorf("customer-facing embeddings usage = %+v, want total_tokens 12", resp.Usage)
	}
	if !strings.Contains(logs, usageIdentityLogPrefix) {
		t.Errorf("expected a loud discrepancy line; logs: %q", logs)
	}
}

// --- end to end: the amount and its provenance, on the same request ---

// TestExecuteSync_WrongUpstreamTotalChargesComponentsAndReportsTheSum is the
// money half. The upstream reports a total six times its own components, the
// same disagreement shape as the live failure, at a magnitude where the
// 1-credit floor cannot make a wrong charge look right.
//
// The settled AMOUNT and its PROVENANCE are asserted together on one request:
// a fix that corrects the number while emptying terminal_usage_confirmed would
// pass an amount-only assertion.
//
// Billing stub note (required check, not decoration): newAccountingMock always
// answers 200, but its family CAN express failure -- newAccountingMockFinalizeFails
// and newAccountingMockReservationFails are the same shape with a status field
// flipped, and are used by the settlement failure tests. No extension needed
// here; this test's subject is what edge-api decides to send, not what
// control-plane does with it.
func TestExecuteSync_WrongUpstreamTotalChargesComponentsAndReportsTheSum(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()
	// 310_000 against components summing to 50_000: issue #1472's 31-versus-5
	// ratio, scaled past the 1-credit floor.
	providerSrv := usageJSONServerWithTotal(40_000, 10_000, 310_000, "hello there")
	defer providerSrv.Close()
	routingSrv := newRoutingMockPriced(catalogHiveFast, PriceUnitTokens)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, providerSrv.URL)
	w := callSyncCtx(orch, context.Background())

	usage := syncResponseUsage(t, w)
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("customer received total_tokens=%d against prompt %d + completion %d",
			usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
	}
	if usage.TotalTokens != 50_000 {
		t.Errorf("total_tokens = %d, want 50000", usage.TotalTokens)
	}

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation; calls seen: %+v", rec.calls)
	}
	want := catalogCredits(t, catalogHiveFast, 40_000, 10_000)
	actual, _ := body["actual_credits"].(float64)
	confirmed, _ := body["terminal_usage_confirmed"].(bool)
	if int64(actual) != want || !confirmed {
		t.Errorf("settlement = {actual_credits: %v, terminal_usage_confirmed: %v}, want {%d, true}: the charge prices the components at the catalog rate, and the upstream did report a real usage block",
			body["actual_credits"], body["terminal_usage_confirmed"], want)
	}
}

// TestExecuteSync_WrongUpstreamTotalDoesNotMoveTheCharge is the control for
// the test above: the same components with an honest total settle at the same
// amount, so the correction is proven not to touch the money in either
// direction.
func TestExecuteSync_WrongUpstreamTotalDoesNotMoveTheCharge(t *testing.T) {
	settle := func(total int64) (int64, bool) {
		rec := &accountingRecorder{}
		acctSrv := newAccountingMock(rec)
		defer acctSrv.Close()
		providerSrv := usageJSONServerWithTotal(40_000, 10_000, total, "hello there")
		defer providerSrv.Close()
		routingSrv := newRoutingMockPriced(catalogHiveFast, PriceUnitTokens)
		defer routingSrv.Close()

		orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, providerSrv.URL)
		callSyncCtx(orch, context.Background())
		body, ok := rec.find("/internal/accounting/reservations/finalize")
		if !ok {
			t.Fatalf("expected FinalizeReservation; calls seen: %+v", rec.calls)
		}
		actual, _ := body["actual_credits"].(float64)
		confirmed, _ := body["terminal_usage_confirmed"].(bool)
		return int64(actual), confirmed
	}

	honest, honestConfirmed := settle(50_000)
	inflated, inflatedConfirmed := settle(310_000)
	if honest != inflated || honestConfirmed != inflatedConfirmed {
		t.Errorf("an inflated upstream total moved settlement: honest {%d, %v} versus inflated {%d, %v}",
			honest, honestConfirmed, inflated, inflatedConfirmed)
	}
}

// TestStreamingRelay_TerminalUsageFrameSatisfiesIdentity covers the streaming
// half, where the usage frame is re-marshalled on its way to the caller.
func TestStreamingRelay_TerminalUsageFrameSatisfiesIdentity(t *testing.T) {
	inflated := `{"id":"gen-abc","created":1787967930,"model":"upstream-route","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":31,"completion_tokens_details":{"reasoning_tokens":26}}}`
	wire := relayWire(t, true, litellmContentFrame, litellmFinishFrame, inflated)
	frames, _, _ := relayedUsage(t, wire)
	if len(frames) == 0 {
		t.Fatalf("no usage frame reached the caller; wire: %q", wire)
	}
	for _, f := range frames {
		u := f.chunk.Usage
		if u.TotalTokens != u.PromptTokens+u.CompletionTokens {
			t.Errorf("relayed usage frame at position %d reports total_tokens=%d against prompt %d + completion %d",
				f.position, u.TotalTokens, u.PromptTokens, u.CompletionTokens)
		}
		if u.CompletionTokens != 1 {
			t.Errorf("completion_tokens = %d, want 1: reasoning tokens are not folded into the billed component (D-055)", u.CompletionTokens)
		}
		// The other half of the same object, which this test used to be
		// silent on. The fixture is the alongside convention (4 + 1 + 26 =
		// 31), so 26 is a measurement, not a breakdown that happens to be too
		// large, and the customer must receive the number the upstream
		// actually reported. An earlier version of this change capped it at 1
		// and handed the caller a fabricated figure.
		if u.CompletionTokensDetails == nil {
			t.Fatalf("relayed usage frame at position %d dropped completion_tokens_details entirely", f.position)
		}
		if u.CompletionTokensDetails.ReasoningTokens != 26 {
			t.Errorf("relayed usage frame at position %d reports reasoning_tokens=%d, want the measured 26",
				f.position, u.CompletionTokensDetails.ReasoningTokens)
		}
	}
}

// --- the shapes the first round of review found uncovered ---

// TestUsageIdentity_ToolCallOnlyTurnCorrectsTheTotalAndRecordsTheGap covers
// the fall-through this change opened at the top of clampZeroCompletionUsage:
// completion_tokens is 0 AND there is no output text to estimate from, so the
// estimate branch does nothing and the identity now runs on the object
// regardless. That is the tool-call-only shape D-055 names by hand
// (chatChoiceTexts reads content and refusal and skips tool_calls), and an
// upstream reporting prompt 200, completion 0, total 260 on it is describing
// 60 tokens of tool-call arguments it counted and attributed to neither
// component.
//
// The intended reading, asserted rather than left to inference: the response
// is corrected DOWN to the component sum, so the 60 leave the customer-facing
// payload as well as the ledger, and the quantity is carried operator-side by
// the log line and the unaccounted-token counter instead. It is an unmeasured
// quantity recorded elsewhere, never a verified zero of tool-call output,
// which is the reading D-056 documents as the trap.
func TestUsageIdentity_ToolCallOnlyTurnCorrectsTheTotalAndRecordsTheGap(t *testing.T) {
	u := &UsageResponse{PromptTokens: 200, CompletionTokens: 0, TotalTokens: 260}
	logs := captureLogs(t, func() {
		// No output text at all: the turn emitted tool calls only.
		clampZeroCompletionUsage(u, nil, "gen-tool", "hive-fast", EndpointChatCompletions)
	})
	if u.CompletionTokens != 0 {
		t.Errorf("completion_tokens = %d, want 0: an estimate of zero is not evidence of output, and inventing one here would bill it", u.CompletionTokens)
	}
	if u.TotalTokens != 200 {
		t.Errorf("total_tokens = %d, want 200 (prompt 200 + completion 0)", u.TotalTokens)
	}
	if strings.Contains(logs, "usage clamp engaged") {
		t.Errorf("the zero-completion estimate must not fire on a turn with no output text; logs: %q", logs)
	}
	if !strings.Contains(logs, usageIdentityLogPrefix) || !strings.Contains(logs, "unaccounted_tokens=60") {
		t.Errorf("the 60 unattributed tokens must be recorded, not merely erased; logs: %q", logs)
	}
}

// --- the two conventions, decided from the wire shape ---

// TestUsageIdentity_AlongsideConventionKeepsTheMeasuredReasoningCount is the
// live #1472 shape with its breakdown present: 4 + 1 + 26 = 31, which is the
// upstream saying, in its own arithmetic, that it counts reasoning ALONGSIDE
// completion_tokens rather than inside it (Google reports totalTokenCount
// inclusive of thoughtsTokenCount while candidatesTokenCount excludes it).
//
// 26 is therefore a measurement, and the customer receives it unchanged. An
// earlier version of this change capped it at completion_tokens, on the theory
// that a breakdown may not exceed the component it breaks down, and handed the
// caller a fabricated 1 while 25 tokens survived only in a log line. Nothing
// in edge-api computes reasoning_tokens; every adapter decodes it verbatim, so
// it carries the upstream's convention rather than this package's assumption
// about what the field means.
//
// The total IS restated, and that is lossless rather than an exception to the
// same rule: the remainder is still on the wire in reasoning_tokens, so a
// caller reading 5 and 26 recovers the upstream's 31 exactly. Nothing measured
// is destroyed, and the quantity is on two counters besides.
func TestUsageIdentity_AlongsideConventionKeepsTheMeasuredReasoningCount(t *testing.T) {
	u := &UsageResponse{
		PromptTokens:            4,
		CompletionTokens:        1,
		TotalTokens:             31,
		CompletionTokensDetails: &CompletionTokensDetails{ReasoningTokens: 26},
	}
	NewStageMetrics(prometheus.NewRegistry())
	unbilled := reasoningTokensUnbilled.WithLabelValues(reasoningAlongside)
	before := testutil.ToFloat64(unbilled)

	logs := captureLogs(t, func() {
		EnforceUsageIdentity(u, "gen-abc", "hive-free", EndpointChatCompletions)
	})

	if u.CompletionTokensDetails.ReasoningTokens != 26 {
		t.Errorf("reasoning_tokens = %d, want the measured 26: a quantity the upstream reported is never shrunk to satisfy an invariant it was not writing under",
			u.CompletionTokensDetails.ReasoningTokens)
	}
	if u.TotalTokens != 5 || u.CompletionTokens != 1 || u.PromptTokens != 4 {
		t.Fatalf("components or total wrong after the restatement: %+v", u)
	}
	if got := testutil.ToFloat64(unbilled); got != before+26 {
		t.Errorf("unbilled reasoning counter = %v, want %v: this is the quantity the owner prices the D-055 decision against, so it cannot live in a log line",
			got, before+26)
	}
	if !strings.Contains(logs, "convention=alongside") {
		t.Errorf("the log must name the convention it decided on; logs: %q", logs)
	}
}

// TestUsageIdentity_UnexplainedBreakdownIsRecordedAndPassedThrough is the
// shape the previous version returned on before it looked at anything:
// prompt 4, completion 1, total 5, reasoning 26. The identity holds, so the
// early return fired, and the impossible breakdown went straight through the
// guard that exists to catch it.
//
// Neither convention describes this object: the inside arithmetic makes the
// breakdown larger than its own component, and the alongside arithmetic wants
// a total of 31. No second field carries a remainder here, so any figure
// written would be invented. The upstream numbers are passed through untouched
// and the fact is recorded as a quantity instead, which on this shape is the
// ONLY record, because the total counters see a discrepancy of zero.
func TestUsageIdentity_UnexplainedBreakdownIsRecordedAndPassedThrough(t *testing.T) {
	u := &UsageResponse{
		PromptTokens:            4,
		CompletionTokens:        1,
		TotalTokens:             5,
		CompletionTokensDetails: &CompletionTokensDetails{ReasoningTokens: 26},
	}
	NewStageMetrics(prometheus.NewRegistry())
	unbilled := reasoningTokensUnbilled.WithLabelValues(reasoningUnexplained)
	before := testutil.ToFloat64(unbilled)

	logs := captureLogs(t, func() {
		EnforceUsageIdentity(u, "gen-abc", "hive-free", EndpointChatCompletions)
	})

	if u.PromptTokens != 4 || u.CompletionTokens != 1 || u.TotalTokens != 5 ||
		u.CompletionTokensDetails.ReasoningTokens != 26 {
		t.Errorf("an object no convention explains was rewritten: %+v (reasoning %d)", u, u.CompletionTokensDetails.ReasoningTokens)
	}
	if got := testutil.ToFloat64(unbilled); got != before+26 {
		t.Errorf("unbilled reasoning counter = %v, want %v: on this shape the total counters record nothing, so silence here is silence everywhere",
			got, before+26)
	}
	if !strings.Contains(logs, "convention=unexplained") {
		t.Errorf("the log must name the classification; logs: %q", logs)
	}
}

// TestUsageIdentity_ReasoningIsNeverRewrittenOnAnUnderReportedTotal covers the
// third position reasoning can be in: a total that falls short of its own
// components, which no convention explains, beside a reasoning count larger
// than completion_tokens. The total is restated and the measurement is not.
//
// The live shape behind this is the zero-completion estimate, where
// completion_tokens is Hive's own guess from the output text and
// reasoning_tokens is the upstream's measurement. Shrinking a measurement to
// fit a guess deletes the better number.
// TestClampZeroCompletionUsage_ReasoningTokensPreserved covers that path end
// to end; this covers the rule directly.
func TestUsageIdentity_ReasoningIsNeverRewrittenOnAnUnderReportedTotal(t *testing.T) {
	u := &UsageResponse{
		PromptTokens:            4,
		CompletionTokens:        1,
		TotalTokens:             3,
		CompletionTokensDetails: &CompletionTokensDetails{ReasoningTokens: 26},
	}
	captureLogs(t, func() {
		EnforceUsageIdentity(u, "gen-abc", "hive-free", EndpointChatCompletions)
	})
	if u.TotalTokens != 5 {
		t.Errorf("total_tokens = %d, want 5", u.TotalTokens)
	}
	if u.CompletionTokensDetails.ReasoningTokens != 26 {
		t.Errorf("reasoning_tokens = %d, want the measured 26", u.CompletionTokensDetails.ReasoningTokens)
	}
}

// TestUsageIdentity_EmbeddingsWithZeroPromptTokensZeroesTheTotal puts the
// zeroing behaviour on the record as a decision rather than a side effect.
//
// Some embedding providers count only a total. Under the identity, which for
// embeddings reduces to total_tokens == prompt_tokens, the correction has
// nowhere to go but down, because raising prompt_tokens to meet the total
// would begin billing tokens nobody metered (D-055). The result is a usage
// block reporting zero for a call that really consumed something. The money
// does not move: settlement already read PromptTokens and already priced the
// 0. The quantity is carried by the log line and the unaccounted-token
// counter, which is the whole reason both exist.
func TestUsageIdentity_EmbeddingsWithZeroPromptTokensZeroesTheTotal(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"route","usage":{"prompt_tokens":0,"total_tokens":97}}`)
	var normalized []byte
	var usage *UsageResponse
	logs := captureLogs(t, func() {
		var err error
		normalized, usage, err = normalizeEmbeddings(body, "hive-embedding-default")
		if err != nil {
			t.Fatalf("normalizeEmbeddings: %v", err)
		}
	})
	if usage == nil || usage.TotalTokens != 0 || usage.PromptTokens != 0 {
		t.Errorf("accounting usage = %+v, want both zero", usage)
	}
	var resp EmbeddingsResponse
	if err := json.Unmarshal(normalized, &resp); err != nil {
		t.Fatalf("decode normalized embeddings: %v", err)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 0 {
		t.Errorf("customer-facing embeddings usage = %+v, want total_tokens 0", resp.Usage)
	}
	if !strings.Contains(logs, "unaccounted_tokens=97") {
		t.Errorf("the deleted 97 must be recorded operator-side, not silently zeroed; logs: %q", logs)
	}
}

// --- the counters ---

// TestUsageIdentity_CorrectionRecordsOccurrenceAndQuantity closes the half of
// the loud-never-silent pair that had no test: the metrics.
//
// Two assertions, because the two counters answer different questions and only
// one of them was here. usageIdentityViolations answers "how often did a total
// disagree"; usageIdentityUnaccountedTokens answers "how many tokens went
// unaccounted", which is the question the correction itself made harder to
// answer by rewriting the total that usage_events.hive_credit_delta used to
// carry (recordCompletedEvent in orchestrator.go, recordInterruptedEvent in
// stream.go).
//
// Asserting the label values incidentally pins the direction split, which
// nothing else does on the over side.
func TestUsageIdentity_CorrectionRecordsOccurrenceAndQuantity(t *testing.T) {
	NewStageMetrics(prometheus.NewRegistry())

	occurrences := usageIdentityViolations.WithLabelValues("hive-free", EndpointChatCompletions, usageIdentityOver)
	quantity := usageIdentityUnaccountedTokens.WithLabelValues(usageIdentityOver)
	occurrencesBefore := testutil.ToFloat64(occurrences)
	quantityBefore := testutil.ToFloat64(quantity)

	u := &UsageResponse{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 31}
	captureLogs(t, func() {
		EnforceUsageIdentity(u, "gen-abc", "hive-free", EndpointChatCompletions)
	})

	if got := testutil.ToFloat64(occurrences); got != occurrencesBefore+1 {
		t.Errorf("occurrence counter = %v, want %v: a declared-but-unincremented counter is indistinguishable from a clean run",
			got, occurrencesBefore+1)
	}
	if got := testutil.ToFloat64(quantity); got != quantityBefore+26 {
		t.Errorf("unaccounted-token counter = %v, want %v: this is the series an operator reads to price the gap, so it carries tokens rather than events",
			got, quantityBefore+26)
	}
}

// TestUsageIdentity_UnaccountedTokenSeriesExistBeforeAnyViolation is the
// absent-reads-as-absent guard.
//
// A CounterVec child does not exist until WithLabelValues creates it, so a
// quantity counter left to appear on first use reports nothing at all until
// the first violation, and "no data" then means either zero violations or a
// counter that was never registered. That is the pair zeroContentCaptureTrips
// already confuses from the other side: declared, incremented on a live path,
// absent from MustRegister, and indistinguishable from a working counter at
// the call site. Both direction series are therefore created at registration,
// so their absence from a scrape is a registration defect rather than a quiet
// clean bill of health.
func TestUsageIdentity_UnaccountedTokenSeriesExistBeforeAnyViolation(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewStageMetrics(reg)
	got, err := testutil.GatherAndCount(reg, "hive_usage_identity_unaccounted_tokens_total")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if got != 2 {
		t.Errorf("the unaccounted-token metric exposes %d series at boot, want 2 (over and under): an operator querying it must not read \"no data\" and take it for zero violations", got)
	}
	// Same requirement for the reasoning counter, and it matters more there:
	// on the unexplained shape nothing is rewritten and the total counters
	// record nothing, so this series is the only evidence the shape was
	// served at all. A series that appears on first use cannot be told apart
	// from one nobody registered.
	unbilled, err := testutil.GatherAndCount(reg, "hive_usage_reasoning_tokens_unbilled_total")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if unbilled != 2 {
		t.Errorf("the unbilled-reasoning metric exposes %d series at boot, want 2 (alongside and unexplained)", unbilled)
	}
}

// --- the relays that hand raw frame bytes to a customer ---

// TestUsageIdentityInFrame_CorrectsTheTotalAndLeavesEveryOtherMemberAlone
// covers the helper both SSE relays use: session chat (which serves the Open
// WebUI front end) and RAG chat. Neither builds a typed usage object, so
// before this helper existed the identity held on four API-key endpoints and
// on neither of those two surfaces.
//
// The untouched-member assertion is the point of patching keys rather than
// re-marshalling a typed UsageResponse: packages/sanitize deliberately keeps
// usage as an open map minus three cost fields, so a round trip through this
// package's struct would silently drop any member it does not declare, which
// would turn a correction helper into a second, invisible sanitiser.
func TestUsageIdentityInFrame_CorrectsTheTotalAndLeavesEveryOtherMemberAlone(t *testing.T) {
	frame := []byte(`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],` +
		`"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":31,` +
		`"completion_tokens_details":{"reasoning_tokens":26,"accepted_prediction_tokens":3},` +
		`"an_upstream_member_this_package_does_not_declare":7}}`)

	var out []byte
	logs := captureLogs(t, func() {
		out = EnforceUsageIdentityInFrame(frame, "gen-abc", "hive-free", EndpointChatCompletions)
	})

	var decoded struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("corrected frame is not valid JSON: %v (%s)", err, out)
	}
	if string(decoded.Usage["total_tokens"]) != "5" {
		t.Errorf("total_tokens = %s, want 5", decoded.Usage["total_tokens"])
	}
	if string(decoded.Usage["an_upstream_member_this_package_does_not_declare"]) != "7" {
		t.Errorf("an undeclared usage member was dropped: %s", out)
	}
	var details map[string]json.RawMessage
	if err := json.Unmarshal(decoded.Usage["completion_tokens_details"], &details); err != nil {
		t.Fatalf("completion_tokens_details is not an object: %v", err)
	}
	// Guard, not coverage: this helper writes total_tokens and nothing else,
	// so it is structurally incapable of touching the breakdown and this
	// assertion passes before and after the convention rework. It goes red if
	// anyone teaches the helper to patch reasoning_tokens, which is the shape
	// the previous round shipped and this one retracts.
	if string(details["reasoning_tokens"]) != "26" {
		t.Errorf("reasoning_tokens = %s, want the measured 26 untouched", details["reasoning_tokens"])
	}
	if string(details["accepted_prediction_tokens"]) != "3" {
		t.Errorf("a sibling breakdown member was dropped: %s", out)
	}
	if !strings.Contains(logs, usageIdentityLogPrefix) {
		t.Errorf("a frame correction must be as loud as a typed one; logs: %q", logs)
	}
}

// TestUsageIdentityInFrame_LeavesAFrameWithoutUsageByteIdentical is the
// hot-path guard: every frame of a stream but the last carries no usage
// member, and a helper that re-encoded them all would rewrite key order and
// number formatting across the entire relay for nothing.
func TestUsageIdentityInFrame_LeavesAFrameWithoutUsageByteIdentical(t *testing.T) {
	frame := []byte(`{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"}}]}`)
	logs := captureLogs(t, func() {
		if got := EnforceUsageIdentityInFrame(frame, "gen-abc", "hive-free", EndpointChatCompletions); string(got) != string(frame) {
			t.Errorf("a frame with no usage member was rewritten:\n got %s\nwant %s", got, frame)
		}
	})
	if strings.Contains(logs, usageIdentityLogPrefix) {
		t.Errorf("a frame with no usage member logged a discrepancy; logs: %q", logs)
	}
}

// TestUsageIdentityInFrame_UnparseableInputIsReturnedUnchanged: a failed
// cosmetic rewrite must never break a frame already committed to the wire.
// Both callers write this return value straight to the client.
func TestUsageIdentityInFrame_UnparseableInputIsReturnedUnchanged(t *testing.T) {
	for name, in := range map[string][]byte{
		"empty":            nil,
		"not json":         []byte(`{"usage":`),
		"null frame":       []byte(`null`),
		"null usage":       []byte(`{"usage":null}`),
		"usage not object": []byte(`{"usage":42}`),
	} {
		t.Run(name, func(t *testing.T) {
			if got := EnforceUsageIdentityInFrame(in, "id", "alias", EndpointChatCompletions); string(got) != string(in) {
				t.Errorf("returned %q, want the original %q", got, in)
			}
		})
	}
}
