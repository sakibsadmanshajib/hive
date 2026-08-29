package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
// completion_tokens was zero. These tests pin the general rule instead: every
// usage object this gateway hands a customer satisfies the identity, whatever
// the upstream reported, and the correction is keyed on the numbers themselves
// rather than on any provider or model family.
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
	}
}
