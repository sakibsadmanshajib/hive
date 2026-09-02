package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- issue #636: a response with no usage block must never settle as a
// confirmed charge ---
//
// The synchronous path used to fall back to the flat reservation estimate
// (10000 credits for chat/completions/responses, 1000 for embeddings) whenever
// the provider omitted the usage block, and sent that number with
// TerminalUsageConfirmed = true. Confirmed means "the upstream reported this
// figure", so the one guard that exists for estimates (the hold clamp plus the
// reconciliation job in control-plane's finalizeLocked) was bypassed, and a
// three-token reply could be billed 10000 credits permanently with nothing
// left to correct it.

// jsonProviderServer stands in for the provider (via LiteLLM) on the
// non-streaming path, answering 200 with a caller-supplied raw body so a test
// can serve a response whose usage block is absent rather than merely zeroed.
func jsonProviderServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
}

// callSyncBody drives the real executeSync lifecycle with an explicit request
// body, so the prompt half of the fallback estimate is exercised rather than
// left empty by a `{}` body.
func callSyncBody(orch *Orchestrator, reqBody string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, []byte(reqBody), "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, 10000, orch.litellm.ChatCompletion, normalizeChatCompletion)
	return w
}

// syncRequestBody is an 11-byte prompt, which the estimator floors at its
// minimum of 1 token for the prompt half of the estimate.
const syncRequestBody = `{"model":"gpt-4o","messages":[{"role":"user","content":"hello world"}]}`

// noUsageResponseBody is a well-formed chat completion with 12,000 bytes of
// assistant content and NO usage field at all: 12000/bytesPerToken = 1000
// tokens for the completion half. Thousands of tokens on purpose -- the
// 1-credit floor makes a small-token assertion pass even when the arithmetic is
// wrong.
func noUsageResponseBody(t *testing.T) string {
	t.Helper()
	content := strings.Repeat("x", 12000)
	body, err := json.Marshal(map[string]any{
		"id":      "chatcmpl-test-no-usage",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   "route",
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	})
	if err != nil {
		t.Fatalf("marshal no-usage response: %v", err)
	}
	return string(body)
}

// TestExecuteSync_NoUsage_NeverConfirmsTheFlatEstimate is the issue #636 bound:
// when the provider returns no usage, the settlement must not be marked
// confirmed and must not charge the flat reservation estimate as though it had
// been measured. It settles instead on a token estimate derived from the bytes
// actually exchanged, flagged unconfirmed so control-plane clamps it to the
// hold and opens a reconciliation job.
func TestExecuteSync_NoUsage_NeverConfirmsTheFlatEstimate(t *testing.T) {
	providerSrv := jsonProviderServer(noUsageResponseBody(t))
	defer providerSrv.Close()
	routingSrv := newRoutingMock(providerSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, providerSrv.URL)
	var w *httptest.ResponseRecorder
	logs := captureLogs(t, func() { w = callSyncBody(orch, syncRequestBody) })

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation for a delivered response; calls seen: %+v", rec.calls)
	}

	confirmed, _ := body["terminal_usage_confirmed"].(bool)
	actual, _ := body["actual_credits"].(float64)

	if confirmed {
		t.Errorf("terminal_usage_confirmed = true for a response that carried no usage block: an estimate must never be recorded as measured truth")
	}
	// The exact number the bound forbids: the flat estimate, confirmed.
	if confirmed && int64(actual) == 10000 {
		t.Errorf("the flat reservation estimate was billed as confirmed usage (issue #636)")
	}
	// The estimate is catalog-priced exactly like a measurement (#688): 1 prompt
	// token (11 bytes, floored at the estimator's minimum) at the fixture's
	// pinned 10500 credits per million plus 1000 completion tokens (12,000 bytes
	// at bytesPerToken) at 42000 is 42_010_500 / 1e6 = 42.01, rounded half up to
	// 42. Only the confidence differs, which is what terminal_usage_confirmed
	// carries. The pinned price is historical on purpose and is not a claim
	// about what hive-fast costs today; see the note at the top of
	// settle_from_catalog_test.go.
	if int64(actual) != 42 {
		t.Errorf("actual_credits = %v, want 42 (hive-fast catalog price for 1 prompt + 1000 completion estimated tokens), never the flat 10000 estimate", body["actual_credits"])
	}
	// The estimated token count charged as credits would be 1001 here (issue
	// #673's estimator times issue #688's missing catalog lookup).
	if int64(actual) == 1001 {
		t.Error("actual_credits = 1001: the estimated token count charged as credits, bypassing the catalog (#688)")
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a charged reservation must never also be released: that refunds a legitimate charge")
	}
	if !strings.Contains(logs, "unconfirmed") {
		t.Errorf("an unmeasured settlement must be loud, not silent; logs: %q", logs)
	}
}

// TestExecuteSync_NoUsage_NoOutput_ReleasesInsteadOfCharging covers the other
// half: nothing measured AND nothing produced means there is no billable
// quantity at all, so the hold is released in full rather than charged at a
// guessed figure. This matches settlementCredits' delivered=false contract on
// the streaming path.
func TestExecuteSync_NoUsage_NoOutput_ReleasesInsteadOfCharging(t *testing.T) {
	providerSrv := jsonProviderServer(`{"id":"chatcmpl-empty","object":"chat.completion","model":"route","choices":[]}`)
	defer providerSrv.Close()
	routingSrv := newRoutingMock(providerSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, providerSrv.URL)
	callSyncBody(orch, syncRequestBody)

	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("nothing was measured and nothing was produced: there is no quantity to charge")
	}
	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected ReleaseReservation so the hold is not stranded; calls seen: %+v", rec.calls)
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("reservation_id = %v, want res-test-1", body["reservation_id"])
	}
}

// TestResponseText covers the completion half of the fallback estimate for
// every endpoint that reaches executeSync. The Responses case matters most: its
// normalized bytes are a ResponseObject, not a chat completion, so a chat-only
// extractor would silently estimate 0 tokens for every unmeasured /v1/responses
// call and settle it as free.
func TestResponseText(t *testing.T) {
	chatNormalized, _, err := normalizeChatCompletion([]byte(
		`{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"chat reply"}}]}`), "alias")
	if err != nil {
		t.Fatalf("normalizeChatCompletion: %v", err)
	}
	completionNormalized, _, err := normalizeCompletion([]byte(
		`{"id":"c2","choices":[{"index":0,"text":"legacy reply"}]}`), "alias")
	if err != nil {
		t.Fatalf("normalizeCompletion: %v", err)
	}
	responsesNormalized, _, err := normalizeResponsesSync([]byte(
		`{"id":"c3","choices":[{"index":0,"message":{"role":"assistant","content":"responses reply"}}]}`),
		"alias", ResponsesRequest{Model: "alias"})
	if err != nil {
		t.Fatalf("normalizeResponsesSync: %v", err)
	}
	embeddingsNormalized, _, err := NormalizeEmbeddings([]byte(
		`{"object":"list","data":[{"index":0,"embedding":[0.1,0.2]}]}`), "alias")
	if err != nil {
		t.Fatalf("NormalizeEmbeddings: %v", err)
	}

	tests := []struct {
		name       string
		endpoint   string
		normalized []byte
		want       string
	}{
		{"chat completions", EndpointChatCompletions, chatNormalized, "chat reply"},
		{"legacy completions", EndpointCompletions, completionNormalized, "legacy reply"},
		{"responses", EndpointResponses, responsesNormalized, "responses reply"},
		{"embeddings carry no generated text", EndpointEmbeddings, embeddingsNormalized, ""},
		{"unparseable body estimates nothing", EndpointChatCompletions, []byte(`not json`), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := responseText(tt.endpoint, tt.normalized); got != tt.want {
				t.Errorf("responseText(%s) = %q, want %q", tt.endpoint, got, tt.want)
			}
		})
	}
}

// TestExecuteSync_ConfirmedUsage_StaysConfirmed guards the direction this fix
// must not break: a genuine upstream usage block is still billed in full and
// still flagged confirmed, so control-plane bills it past the flat hold
// instead of clamping a fact (the trap the PR #602 review caught).
func TestExecuteSync_ConfirmedUsage_StaysConfirmed(t *testing.T) {
	var hits int64
	providerSrv := countingJSONServer(&hits)
	defer providerSrv.Close()
	routingSrv := newRoutingMock(providerSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, providerSrv.URL)
	callSyncBody(orch, syncRequestBody)

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation on a confirmed-usage completion; calls seen: %+v", rec.calls)
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); !confirmed {
		t.Errorf("terminal_usage_confirmed = %v, want true: the upstream did report usage", body["terminal_usage_confirmed"])
	}
	if actual, _ := body["actual_credits"].(float64); int64(actual) != 1 {
		t.Errorf("actual_credits = %v, want 1 (the catalog price for the upstream's own reported tokens, floored)", body["actual_credits"])
	}
}
