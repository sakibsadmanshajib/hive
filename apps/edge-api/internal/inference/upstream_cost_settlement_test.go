package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// End-to-end settlement for a variable-price alias, exercised through the same
// httptest wiring the catalog-price tests use: a routing mock, an accounting
// mock that records every call, and an upstream that answers like OpenRouter
// through LiteLLM.
//
// The unit tests next door prove the arithmetic. These prove the WIRING, which
// is where this could still be wrong while every unit test stays green: the raw
// bytes have to survive from the upstream response to the settlement call, and
// the hold has to come from the catalog rather than the endpoint default.

// openrouterAutoPricing is the openrouter-auto catalog row: no fixed price, a
// 2,000,000,000 credit hold: the catalog figure for openrouter-auto at the
// current unit ($2.00 equivalent; seeded 200000 by
// supabase/migrations/20260822_30_...sql and rescaled by 10000 in
// 20260823_40_credit_unit_rescale_billion.sql).
var openrouterAutoPricing = UpstreamActualPricing(2_000_000_000)

// autoHoldForAnEmptyBody is the hold these tests expect, in credits. Every one
// of them dispatches the body `{}`, which EnforceVariablePriceBounds turns into
// a 50-byte body carrying both completion-ceiling spellings at 16384. Sized at
// the route's own rate ceiling (3.00 and 15.00 USD per million, see
// deploy/litellm/config.yaml and VariablePricePromptCeilingUSD):
//
//	prompt:     50 bytes, a rigorous upper bound on prompt tokens
//	                       50 x 3.00 / 1e6            = 0.000150 USD
//	completion: 16384 x 15.00 / 1e6                   = 0.245760 USD
//	total 0.245910 USD x 1e9 credits/USD              = 245,910,000
//
// No margin factor in that product: D-064 retired the 1.4 multiplier on
// 2026-09-02, and the hold goes through the same CreditsForUpstreamCost the
// charge does, so it lost the factor at the same moment the charge did.
//
// It is asserted as an exact figure rather than "not the endpoint default", so
// a change to either bound or either ceiling fails here and has to be
// re-derived rather than sliding through. It is nowhere near the flat endpoint
// default (10000 in these fixtures), the token count, or the catalog's
// whole-envelope hold (2e9), so a regression to any of those shapes fails too.
const autoHoldForAnEmptyBody = 245_910_000

// newRoutingMockVariable answers route selection with a variable-price alias.
func newRoutingMockVariable() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:          "openrouter-auto",
			RouteID:          "route-openrouter-auto-beta",
			LiteLLMModelName: "route-openrouter-auto-beta",
			Provider:         "openrouter",
			Pricing:          openrouterAutoPricing,
			PriceUnit:        "tokens",
		})
	}))
}

// newEchoingAccountingMock echoes back the hold that was actually requested,
// unlike newAccountingMock which returns a fixed 10000. The echo matters here:
// settlement charges the hold when it cannot read a cost, so a mock that lied
// about the hold size would make the fail-closed assertions meaningless.
//
// It answers with `reserved_credits`, which is the key the real control plane
// publishes (apps/control-plane/internal/accounting/types.go). An earlier
// version of this mock answered `estimated_credits`, a key nothing sends, and
// that single wrong word made every hold assertion here pass against a value
// production would have decoded as 0. The mock has to speak the real wire shape
// or it is testing itself.
func newEchoingAccountingMock(rec *accountingRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/internal/accounting/reservations":
			estimated, _ := body["estimated_credits"].(float64)
			// Encoded as a raw map, not ReservationResult, so the test cannot
			// accidentally start passing because the Go struct gained a field.
			// This is the JSON the control plane really writes.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "res-test-1", "account_id": "acct-test-1", "status": "active",
				"reserved_credits": int64(estimated),
			})
		case "/internal/usage/attempts":
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "streaming",
			})
		}
	}))
}

// openrouterSSEServer streams like OpenRouter through LiteLLM. The terminal
// usage frame is written as RAW JSON rather than built from ChatCompletionChunk
// on purpose: our own struct has no cost field, so building the fixture from it
// could not produce the bytes this feature has to read.
//
// costJSON is spliced in verbatim so a test can send a real number, a null, a
// string, or omit the key entirely.
func openrouterSSEServer(costJSON string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// A content chunk that also names the upstream model the router chose.
		// That name must never reach the client.
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"gen-1755900000-AUDIT","object":"chat.completion.chunk",`+
			`"created":1700000000,"model":"anthropic/claude-sonnet-4.5","provider":"Anthropic",`+
			`"choices":[{"index":0,"delta":{"role":"assistant","content":"hello there"},"finish_reason":null}]}`)
		flusher.Flush()

		costField := ""
		if costJSON != "" {
			costField = `,"cost":` + costJSON
		}
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"gen-1755900000-AUDIT","object":"chat.completion.chunk",`+
			`"created":1700000000,"model":"anthropic/claude-sonnet-4.5","provider":"Anthropic",`+
			`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500`+costField+`}}`)
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// runVariableStreaming drives executeStreaming and hands back the recorder so a
// test can inspect exactly what the CLIENT saw, which is where a cost or a
// chosen-model leak would show up.
func runVariableStreaming(orch *Orchestrator) (<-chan struct{}, *headerCommitRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := newHeaderCommitRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`),
			"openrouter-auto", "openrouter-auto",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil,
			orch.litellm.ChatCompletion)
	}()
	return done, w
}

// The happy path: the reported cost becomes the charge, at the right magnitude.
func TestVariablePriceStreaming_SettlesAtTheReportedUpstreamCost(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newEchoingAccountingMock(rec)
	defer acctSrv.Close()
	litellmSrv := openrouterSSEServer("0.0123456")
	defer litellmSrv.Close()
	routingSrv := newRoutingMockVariable()
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	done, _ := runVariableStreaming(orch)
	waitDone(t, done)

	// The hold must be sized from the variable-price bounds, not from the flat
	// 10000 the endpoint passes in. If ReservationCredits were skipped this
	// reads 10000. See autoHoldForAnEmptyBody for the arithmetic.
	reservation, ok := rec.find("/internal/accounting/reservations")
	if !ok {
		t.Fatalf("no reservation was created; calls: %+v", rec.calls)
	}
	if held, _ := reservation["estimated_credits"].(float64); int64(held) != autoHoldForAnEmptyBody {
		t.Errorf("hold = %v, want %d; a router request cannot be held at the flat endpoint default",
			reservation["estimated_credits"], autoHoldForAnEmptyBody)
	}

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected a finalize on a normal completion; calls: %+v", rec.calls)
	}
	// 0.0123456 USD x 1e9 credits per USD = 12,345,600 exactly.
	// Asserted against the KNOWN upstream cost. Note this is nowhere near the
	// token count (1500) nor the hold (2e9), so a regression to either
	// shape fails here rather than sliding past a non-zero check.
	if actual, _ := body["actual_credits"].(float64); int64(actual) != 12_345_600 {
		t.Errorf("actual_credits = %v, want 12345600 for a reported cost of 0.0123456 USD", body["actual_credits"])
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); !confirmed {
		t.Error("a charge derived from a real reported cost is measured truth and must be confirmed")
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a charged reservation must never also be released")
	}
}

// The fail-closed path, end to end. This is the one that matters: on the
// version of LiteLLM this repo pins today, the streaming terminal chunk carries
// NO cost, so this is not a hypothetical shape.
func TestVariablePriceStreaming_MissingCostChargesTheHoldNotZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		cost string
	}{
		{"cost key absent entirely, which is what our pinned LiteLLM produces", ""},
		{"cost explicitly null", "null"},
		{"cost is a confident zero", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &accountingRecorder{}
			acctSrv := newEchoingAccountingMock(rec)
			defer acctSrv.Close()
			litellmSrv := openrouterSSEServer(tc.cost)
			defer litellmSrv.Close()
			routingSrv := newRoutingMockVariable()
			defer routingSrv.Close()

			orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
			done, _ := runVariableStreaming(orch)
			waitDone(t, done)

			body, ok := rec.find("/internal/accounting/reservations/finalize")
			if !ok {
				t.Fatalf("a delivered response must still settle; calls: %+v", rec.calls)
			}
			actual, _ := body["actual_credits"].(float64)
			if int64(actual) == 0 {
				t.Fatal("settled at ZERO with no readable upstream cost: this is the free-serve bug")
			}
			if int64(actual) != autoHoldForAnEmptyBody {
				t.Errorf("actual_credits = %v, want the full hold %d", body["actual_credits"], autoHoldForAnEmptyBody)
			}
			if confirmed, _ := body["terminal_usage_confirmed"].(bool); confirmed {
				t.Error("a charge with no readable cost must NOT be flagged as measured truth")
			}
		})
	}
}

// Provider-blindness. The owner deliberately named this alias after its
// provider, so the alias name is not secret. The identity of the model the
// router actually chose still is, and so is what we paid for it.
func TestVariablePriceStreaming_LeaksNeitherCostNorChosenModelToTheClient(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newEchoingAccountingMock(rec)
	defer acctSrv.Close()
	litellmSrv := openrouterSSEServer("0.0123456")
	defer litellmSrv.Close()
	routingSrv := newRoutingMockVariable()
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	done, w := runVariableStreaming(orch)
	waitDone(t, done)

	client := w.Body.String()
	if client == "" {
		t.Fatal("client received nothing, so this test would pass vacuously")
	}
	// Sanity: the stream really did reach the client, so the absence checks
	// below mean something.
	if !strings.Contains(client, "hello there") {
		t.Fatalf("expected the completion text to reach the client, got: %s", client)
	}

	for _, forbidden := range []string{
		"anthropic/claude-sonnet-4.5", // the model the router chose
		"Anthropic",                   // the upstream provider
		"0.0123456",                   // what we paid
		"\"cost\"",                    // the cost field itself
		"cost_details",
	} {
		if strings.Contains(client, forbidden) {
			t.Errorf("client response leaked %q; full body: %s", forbidden, client)
		}
	}

	// The same information must still be recoverable internally, or a charge
	// is unauditable. The generation id is the audit handle.
	if !strings.Contains(client, "openrouter-auto") {
		t.Error("the client should still see the alias it asked for")
	}
}

// The Responses streaming path has its own relay loop, and it was missing the
// raw-usage capture entirely, so a variable-price alias could only ever fail
// closed there. Same assertion as the chat-completions path: the charge must be
// the reported cost, not the hold.
func TestResponsesStreamingSettlesAtTheReportedUpstreamCost(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newEchoingAccountingMock(rec)
	defer acctSrv.Close()
	litellmSrv := openrouterSSEServer("0.0123456")
	defer litellmSrv.Close()
	routingSrv := newRoutingMockVariable()
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := newHeaderCommitRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		orch.executeResponsesStreaming(context.Background(), w, req, []byte(`{}`),
			ResponsesRequest{Model: "openrouter-auto"}, "openrouter-auto",
			NeedFlags{NeedResponses: true, NeedStreaming: true}, 10000)
	}()
	waitDone(t, done)

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected a finalize on the responses path; calls: %+v", rec.calls)
	}
	// 0.0123456 x 1e9 = 12,345,600 exactly. If the capture is missing this
	// reads the hold (2e9), because the cost was never seen.
	if actual, _ := body["actual_credits"].(float64); int64(actual) != 12_345_600 {
		t.Errorf("actual_credits = %v, want 12345600. The responses relay must capture the raw usage frame too.",
			body["actual_credits"])
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); !confirmed {
		t.Error("a charge from a real reported cost must be confirmed")
	}
}
