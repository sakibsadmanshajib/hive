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
)

// --- issue #688: the settled charge must come from the alias's catalog row ---
//
// Every chat, completions, responses and embeddings request used to settle at
// one credit per token: settlementCredits returned the provider's total_tokens
// and executeSync/settleStream passed that straight through as ActualCredits.
// At 100000 credits per USD that is 10.00 USD per million tokens on every
// alias, two orders of magnitude above hive-fast's published input price, and
// it ignored model_aliases entirely.
//
// The catalog figures below are HISTORICAL ON PURPOSE: they are the rows
// 20260801_01_alias_pricing_correction.sql shipped, in credits per MILLION
// tokens, and hive-fast has since been repriced to 7000 / 11200 by
// 20260801_14_route_groq_fast_cheapest_model.sql. They are deliberately not
// refreshed. Prices arrive from the database at runtime, so what these tests
// prove is that whatever price the resolved route carries is the price actually
// paid; pinning one fixed row keeps that proof from having to be re-derived
// every time the catalog is repriced. Every expectation in this file is derived
// longhand from these numbers with the same arithmetic metering.ChargeCredits
// implements, so a test fails if the conversion stops honouring the route's
// price, and no expectation here is a claim about what hive-fast costs today.
//
// Every pricing assertion uses THOUSANDS of tokens on purpose: the 1-credit
// floor (a request that consumed real work is never free) makes a small-token
// assertion pass even when the conversion is wrong by orders of magnitude.

// catalogHiveFast is the hive-fast row as of migration _01: 0.075 / 0.300 USD
// per million upstream, times a 1.4 margin, times 100000 credits per USD. See
// the note above on why this pinned historical row is not refreshed.
var catalogHiveFast = SelectRoutePricing{InputPriceCredits: 10_500, OutputPriceCredits: 42_000}

// catalogHiveAuto is the hive-auto row: 0.400 / 1.600 USD per million upstream.
var catalogHiveAuto = SelectRoutePricing{InputPriceCredits: 56_000, OutputPriceCredits: 224_000}

// catalogHiveSTT is hive-stt, priced per SECOND of audio rather than per token
// (supabase/migrations/20260801_13_alias_price_unit.sql). No token-metered
// endpoint can charge against it without inventing a conversion, so it stands
// in for the fail-closed case.
var catalogHiveSTT = SelectRoutePricing{OutputPriceCredits: 4_316_667}

// newRoutingMockPriced stands in for the control-plane's route-selection
// endpoint, answering with an explicit catalog price and price unit -- what the
// real endpoint has sent since PR #651 and PR #671 (routing.Service refuses an
// alias with no usable price outright, so an unpriced payload is not a shape
// production can produce).
func newRoutingMockPriced(pricing SelectRoutePricing, priceUnit string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:          "hive-fast",
			RouteID:          "route-test-1",
			LiteLLMModelName: "route-groq-fast",
			Provider:         "groq",
			Pricing:          pricing,
			PriceUnit:        priceUnit,
		})
	}))
}

// usageSSEServer streams a full completion whose terminal usage chunk reports
// exactly the token counts asked for, so a test can assert a charge against
// known input and output quantities.
func usageSSEServer(promptTokens, completionTokens int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		stop := "stop"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "hello there", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "", &stop))
		flusher.Flush()
		usageChunk := ChatCompletionChunk{
			ID:      "chunk-3",
			Object:  "chat.completion.chunk",
			Created: 1700000000,
			Model:   "route",
			Choices: []ChunkChoice{},
			Usage: &UsageResponse{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
		}
		b, _ := json.Marshal(usageChunk)
		fmt.Fprintln(w, "data: "+string(b))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// usageJSONServer answers the non-streaming path with a completion whose usage
// block reports the given token counts.
func usageJSONServer(promptTokens, completionTokens int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:     "chatcmpl-test-1",
			Object: "chat.completion",
			Model:  "route",
			Usage: &UsageResponse{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
		})
	}))
}

// catalogCredits is the charge the catalog row implies for the given token
// counts, spelled out longhand rather than by calling the production helper, so
// this file's expectations cannot drift with the implementation they check:
// (input * input_price + output * output_price) / 1_000_000, rounded half up.
func catalogCredits(t *testing.T, pricing SelectRoutePricing, inputTokens, outputTokens int64) int64 {
	t.Helper()
	numerator := inputTokens*pricing.InputPriceCredits + outputTokens*pricing.OutputPriceCredits
	credits := numerator / 1_000_000
	if numerator%1_000_000*2 >= 1_000_000 {
		credits++
	}
	if inputTokens+outputTokens > 0 && credits < 1 {
		credits = 1
	}
	return credits
}

// TestExecuteStreaming_SettlesFromCatalogPrice is the primary bound: for known
// input and output token counts the settled charge equals what the alias's
// catalog row implies, not the token count.
func TestExecuteStreaming_SettlesFromCatalogPrice(t *testing.T) {
	cases := []struct {
		name             string
		pricing          SelectRoutePricing
		promptTokens     int64
		completionTokens int64
	}{
		{"hive-fast", catalogHiveFast, 12_000, 3_000},
		{"hive-auto", catalogHiveAuto, 12_000, 3_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &accountingRecorder{}
			acctSrv := newAccountingMock(rec)
			defer acctSrv.Close()
			litellmSrv := usageSSEServer(tc.promptTokens, tc.completionTokens)
			defer litellmSrv.Close()
			routingSrv := newRoutingMockPriced(tc.pricing, "tokens")
			defer routingSrv.Close()

			orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
			done, _ := runExecuteStreaming(orch, context.Background())
			waitDone(t, done)

			body, ok := rec.find("/internal/accounting/reservations/finalize")
			if !ok {
				t.Fatalf("expected FinalizeReservation on a normal completion; calls seen: %+v", rec.calls)
			}
			want := catalogCredits(t, tc.pricing, tc.promptTokens, tc.completionTokens)
			actual, _ := body["actual_credits"].(float64)
			if int64(actual) != want {
				t.Errorf("actual_credits = %v, want %d (the alias's catalog price for %d input + %d output tokens)",
					body["actual_credits"], want, tc.promptTokens, tc.completionTokens)
			}
			if int64(actual) == tc.promptTokens+tc.completionTokens {
				t.Errorf("actual_credits = %v: still one credit per token, the catalog was never consulted (#688)", body["actual_credits"])
			}
			if confirmed, _ := body["terminal_usage_confirmed"].(bool); !confirmed {
				t.Error("terminal_usage_confirmed must stay true: the upstream reported a real usage block")
			}
			if rec.has("/internal/accounting/reservations/release") {
				t.Error("a charged reservation must never also be released")
			}
		})
	}
}

// TestExecuteStreaming_DoesNotSettleAtTokenCount pins the exact figure the bug
// report carried: a 72-input, 31-output request settled at 103 credits, the sum
// of its tokens. At the pinned hive-fast price above that same request is worth
// 2 credits.
func TestExecuteStreaming_DoesNotSettleAtTokenCount(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()
	litellmSrv := usageSSEServer(72, 31)
	defer litellmSrv.Close()
	routingSrv := newRoutingMockPriced(catalogHiveFast, "tokens")
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation; calls seen: %+v", rec.calls)
	}
	actual, _ := body["actual_credits"].(float64)
	if int64(actual) == 103 {
		t.Error("actual_credits = 103: exactly 72 + 31 tokens, the one-credit-per-token charge #688 reported")
	}
	if want := catalogCredits(t, catalogHiveFast, 72, 31); int64(actual) != want {
		t.Errorf("actual_credits = %v, want %d (hive-fast catalog price)", body["actual_credits"], want)
	}
}

// TestExecuteSync_SettlesFromCatalogPrice covers the non-streaming path, which
// carried the same defect through its own settlement call.
func TestExecuteSync_SettlesFromCatalogPrice(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()
	providerSrv := usageJSONServer(12_000, 3_000)
	defer providerSrv.Close()
	routingSrv := newRoutingMockPriced(catalogHiveFast, "tokens")
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, providerSrv.URL)
	callSyncCtx(orch, context.Background())

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation; calls seen: %+v", rec.calls)
	}
	want := catalogCredits(t, catalogHiveFast, 12_000, 3_000)
	actual, _ := body["actual_credits"].(float64)
	if int64(actual) != want {
		t.Errorf("actual_credits = %v, want %d (hive-fast catalog price)", body["actual_credits"], want)
	}
	if int64(actual) == 15_000 {
		t.Error("actual_credits = 15000: still the raw token total (#688)")
	}
}

// TestExecuteStreaming_NonTokenPricedAlias_RefusesBeforeReserving is the
// fail-closed half (D-034): an alias priced in a unit this endpoint cannot
// meter must be refused before any hold is taken and before the request ever
// reaches a provider. Converting seconds into tokens would invent a rate;
// serving it anyway would serve it free.
func TestExecuteStreaming_NonTokenPricedAlias_RefusesBeforeReserving(t *testing.T) {
	var hits int64
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()
	litellmSrv := countingSSEServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMockPriced(catalogHiveSTT, "seconds")
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	if rec.has("/internal/accounting/reservations") {
		t.Error("a request that cannot be priced must not create a reservation")
	}
	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("a request that cannot be priced must not be charged")
	}
	if atomic.LoadInt64(&hits) != 0 {
		t.Errorf("upstream was dispatched %d times: an unpriceable request must never reach a provider", hits)
	}
}

// TestExecuteSync_NonTokenPricedAlias_RefusesBeforeReserving is the same
// fail-closed bound on the non-streaming path.
func TestExecuteSync_NonTokenPricedAlias_RefusesBeforeReserving(t *testing.T) {
	var hits int64
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()
	providerSrv := countingJSONServer(&hits)
	defer providerSrv.Close()
	routingSrv := newRoutingMockPriced(catalogHiveSTT, "seconds")
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, providerSrv.URL)
	w := callSyncCtx(orch, context.Background())

	if w.Code == http.StatusOK {
		t.Errorf("status = 200: an unpriceable request must be refused, not served (body: %s)", w.Body.String())
	}
	if rec.has("/internal/accounting/reservations") {
		t.Error("a request that cannot be priced must not create a reservation")
	}
	if atomic.LoadInt64(&hits) != 0 {
		t.Errorf("upstream was dispatched %d times: an unpriceable request must never reach a provider", hits)
	}
	if body := strings.ToLower(w.Body.String()); body != "" {
		for _, leak := range []string{"groq", "openrouter", "usd", "exchange"} {
			if strings.Contains(body, leak) {
				t.Errorf("refusal leaked %q to the customer: %s", leak, w.Body.String())
			}
		}
	}
}
