package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// hiveAutoHoldCredits is hive-auto's real reservation_estimate_credits as of
// the 2026-08-29 catalog: 2,000,000,000 credits, 2 USD. hive-auto is the only
// alias that owns a supports_batch route, so this is the hold every
// fail-closed batch settlement actually charges today.
const hiveAutoHoldCredits int64 = 2_000_000_000

// upstreamActual builds hive-auto's pricing shape: no price columns, a hold.
func upstreamActual() catalog.CatalogPricing {
	return catalog.UpstreamActualPricing(hiveAutoHoldCredits)
}

// body builds a raw upstream chat-completion body with an optional cost field,
// the shape LiteLLM relays from OpenRouter before packages/sanitize strips it.
func body(t *testing.T, generationID string, cost string, promptTokens, completionTokens int64) []byte {
	t.Helper()
	usage := map[string]any{"prompt_tokens": promptTokens, "completion_tokens": completionTokens}
	if cost != "" {
		usage["cost"] = json.RawMessage(cost)
	}
	raw, err := json.Marshal(map[string]any{
		"id":      generationID,
		"choices": []any{map[string]any{"index": 0, "message": map[string]string{"content": "hi"}}},
		"usage":   usage,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return raw
}

// TestDefaultCreditPolicy_NeverBillsOnTokenTotal is the regression guard for
// the second defect folded into issue #1473, across every reasoning-token
// convention an upstream is known to use.
//
// Both rates are deliberately one credit per token, identical to the flat
// formula settlement used to apply, so the only thing this test can measure is
// the QUANTITY billed. The charge is prompt plus completion in every row, and
// total_tokens is never the charge in any of them.
//
// The conventions cannot be told apart by model family, only by the numbers,
// which is why the code detects them from the identity rather than from a name.
func TestDefaultCreditPolicy_NeverBillsOnTokenTotal(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		prompt, completion, reasoning, total, wantCharge int64
		wantReason                                       string
	}{
		// Measured live on this pool: Google counts thoughts in the total and
		// outside the candidates, so completion_tokens EXCLUDES the 26
		// reasoning tokens. 4 + 1 + 26 = 31. The customer received 5 tokens and
		// the old formula charged 31, the 6.2x overcharge this issue names.
		{"reasoning alongside completion", 4, 1, 26, 31, 5, "catalog_price"},
		// OpenAI's o-series convention: completion_tokens already CONTAINS the
		// reasoning tokens, and the upstream billed us for them as output.
		// 4 + 27 = 31. Charging 31 here is cost recovery rather than an
		// overcharge, and it happens to equal the total, which is fine: the
		// charge is still derived from the components, never read off the total.
		{"reasoning inside completion", 4, 27, 26, 31, 31, "catalog_price"},
		// No reasoning at all, components explain the total exactly.
		{"no reasoning reported", 8, 5, 0, 13, 13, "catalog_price"},
		// Neither identity holds: 8 + 5 = 13 and 8 + 5 + 0 = 13, neither of
		// which is 31. A third shape nobody has characterised. The charge is
		// STILL the components and still never the total; only the label
		// changes, so the unrecognised shape reaches a log instead of settling
		// silently.
		{"neither identity holds", 8, 5, 0, 31, 13, "catalog_price_unexplained_total"},
		// An upstream that reports no total at all has no identity to violate,
		// so it must not be labelled as violating one.
		{"no total reported", 8, 5, 0, 0, 13, "catalog_price"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DefaultCreditPolicy{}.Credits(testFixedPricing(), &Usage{
				PromptTokens:     tc.prompt,
				CompletionTokens: tc.completion,
				ReasoningTokens:  tc.reasoning,
				TotalTokens:      tc.total,
			}, nil)
			if err != nil {
				t.Fatalf("priced line refused: %v", err)
			}
			want := LinePrice{Credits: tc.wantCharge, Confirmed: true, Reason: tc.wantReason}
			if got != want {
				t.Fatalf("settled %+v, want %+v: the charge must be prompt plus completion, "+
					"never the reported total of %d", got, want, tc.total)
			}
		})
	}
}

// TestDefaultCreditPolicy_PricesAtTheAliasRate is the regression guard for the
// first defect. hive-default's catalog rate is 89,460,000 credits per million
// input tokens and 357,840,000 per million output, so 1000 prompt plus 500
// completion is 268,380 credits. The flat formula charged 1500, roughly a
// ninetieth of the catalog rate.
//
// Amount and provenance are asserted together on one value, so a regression
// that restores the number while emptying the handle still fails.
func TestDefaultCreditPolicy_PricesAtTheAliasRate(t *testing.T) {
	const inputRate, outputRate int64 = 89_460_000, 357_840_000
	got, err := DefaultCreditPolicy{}.Credits(catalog.FixedPricing(inputRate, outputRate),
		&Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}, nil)
	if err != nil {
		t.Fatalf("priced line refused: %v", err)
	}
	want := LinePrice{Credits: (1000*inputRate + 500*outputRate) / 1_000_000, Confirmed: true, Reason: "catalog_price"}
	if got != want {
		t.Fatalf("settled %+v, want %+v: settlement ignored the alias price", got, want)
	}
}

// TestDefaultCreditPolicy_FixedPriceRoundsHalfUpOnce checks the arithmetic
// this package had to reimplement matches D-031: components summed first, one
// division by a million, round half up, and a nonzero quantity floored at one
// credit so a line that consumed real work is never free.
func TestDefaultCreditPolicy_FixedPriceRoundsHalfUpOnce(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		inRate, outRate          int64
		promptTokens, compTokens int64
		want                     int64
	}{
		// 1 x 500,000 + 1 x 500,000 = 1,000,000 / 1e6 = exactly 1.
		{"exact", 500_000, 500_000, 1, 1, 1},
		// 1 x 500,000 / 1e6 = 0.5, rounds half UP to 1.
		{"half rounds up", 500_000, 0, 1, 0, 1},
		// 1 x 499,999 / 1e6 = 0.499999, rounds down to 0, then the floor lifts
		// it to 1 because a token was actually consumed.
		{"below half floors at one", 499_999, 0, 1, 0, 1},
		// Summed first: 3 x 400,000 = 1,200,000 / 1e6 = 1.2, rounds to 1. Two
		// independent per-component roundings would give 0+0 or 1+1 instead.
		{"sums before dividing", 400_000, 400_000, 2, 1, 1},
		// A zero output rate is legitimate (input-only metering) and must not
		// be read as "no price".
		{"one side priced", 1_000_000, 0, 10, 7, 10},
		// A negative rate is a catalog defect, not a discount. It is clamped to
		// zero so one component can never subtract from another: without the
		// clamp this row settles at 10 - 7 = 3 credits instead of 10.
		{"negative rate cannot subtract", 1_000_000, -1_000_000, 10, 7, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DefaultCreditPolicy{}.Credits(catalog.FixedPricing(tc.inRate, tc.outRate),
				&Usage{PromptTokens: tc.promptTokens, CompletionTokens: tc.compTokens}, nil)
			if err != nil {
				t.Fatalf("priced line refused: %v", err)
			}
			if got.Credits != tc.want {
				t.Fatalf("credits=%d want %d", got.Credits, tc.want)
			}
		})
	}
}

// TestDefaultCreditPolicy_UpstreamActualSettlesFromReportedCost covers the one
// alias that can actually reach the batch path today. hive-auto is
// upstream_actual with NULL price columns, so there is no catalog rate to
// charge and no token quantity that means anything: the charge is the cost the
// provider reported for that specific generation, times the 7/5 margin, times
// 1e9 credits per USD.
func TestDefaultCreditPolicy_UpstreamActualSettlesFromReportedCost(t *testing.T) {
	for _, tc := range []struct {
		name string
		cost string
		want int64
	}{
		// 0.0001 USD x 1.4 x 1e9 = 140,000 credits, exact.
		{"ordinary cost", "0.0001", 140_000},
		// 0.0000000001 USD x 1.4 x 1e9 = 0.14, which rounds to zero and is
		// then floored at one credit: a line that cost real money is never
		// settled free.
		{"sub-credit cost floors at one", "0.0000000001", 1},
		// Just inside the 10 USD per-line ceiling: 7.142857142 x 1.4 x 1e9 is
		// 9,999,999,998.8, which rounds half up to 9,999,999,999 and settles
		// rather than being refused.
		{"just inside the ceiling", "7.142857142", 9_999_999_999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DefaultCreditPolicy{}.Credits(upstreamActual(), nil,
				body(t, "gen-abc123", tc.cost, 8, 5))
			if err != nil {
				t.Fatalf("priced line refused: %v", err)
			}
			want := LinePrice{Credits: tc.want, Confirmed: true, Reason: "upstream_cost", GenerationID: "gen-abc123"}
			if got != want {
				t.Fatalf("settled %+v, want %+v", got, want)
			}
		})
	}
}

// TestDefaultCreditPolicy_UpstreamActualFailsClosedToTheHold pins what failing
// closed means once the work has already been done (D-034).
//
// The upstream call has been made, we have paid for it, and the completion is
// already on its way into output.jsonl, so there is nothing left to refuse.
// Failing closed therefore means charging the alias's own per-request hold,
// unconfirmed, with a reason naming the exact failure. Never zero, and never
// the token guess the old formula produced. executor.Settle still clamps the
// batch total at the customer's reservation, so this can never charge past
// what was held.
func TestDefaultCreditPolicy_UpstreamActualFailsClosedToTheHold(t *testing.T) {
	oversizedLiteral := "0." + strings.Repeat("1", 70)
	for _, tc := range []struct {
		name       string
		raw        []byte
		wantReason string
	}{
		{"cost field absent", body(t, "gen-1", "", 8, 5), "upstream_cost_absent"},
		{"no usage object at all", []byte(`{"id":"gen-1","choices":[]}`), "upstream_cost_absent"},
		{"empty body", nil, "upstream_cost_absent"},
		// A confident zero alongside real tokens is refused rather than
		// treated as free, exactly as the sync path refuses it: we cannot tell
		// a genuinely free upstream from a broken cost field, and three days of
		// unmetered serving in July 2026 is the precedent for which way to err.
		{"zero cost with tokens", body(t, "gen-1", "0", 8, 5), "upstream_cost_zero"},
		{"negative cost", body(t, "gen-1", "-0.5", 8, 5), "upstream_cost_negative"},
		{"unparseable cost", []byte(`{"id":"gen-1","usage":{"cost":"not-a-number","prompt_tokens":8,"completion_tokens":5}}`), "upstream_cost_unparseable"},
		{"cost literal past the length cap", body(t, "gen-1", oversizedLiteral, 8, 5), "upstream_cost_unparseable"},
		// 100 USD x 1.4 = 140,000,000,000 credits, past the 10 USD per-line
		// ceiling. Refused rather than clamped, so whatever produced an absurd
		// figure stays visible instead of being quietly capped.
		{"implausibly large cost", body(t, "gen-1", "100", 8, 5), "upstream_cost_implausible"},
		{"malformed json", []byte(`{"id":`), "upstream_cost_unparseable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DefaultCreditPolicy{}.Credits(upstreamActual(), &Usage{PromptTokens: 8, CompletionTokens: 5, TotalTokens: 31}, tc.raw)
			if err != nil {
				t.Fatalf("a priceable alias was refused instead of failing closed to its hold: %v", err)
			}
			if got.Credits != hiveAutoHoldCredits {
				t.Fatalf("charged %d, want the %d hold: a failed cost read must never settle at a token count or at zero",
					got.Credits, hiveAutoHoldCredits)
			}
			if got.Confirmed {
				t.Fatalf("settlement %+v flagged confirmed, but no upstream figure was readable", got)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("reason=%q want %q: the failure kinds are kept apart so a log can tell "+
					"a missing field from a field that said zero", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestDefaultCreditPolicy_RefusesAnUnpriceableLine covers the shapes where
// there is no defensible figure to charge at all. They are unreachable today,
// but for two DIFFERENT reasons, and conflating them would leave the next
// person trusting a guarantee that does not hold:
//
//   - The first four rows are ALIAS SHAPES. routing.SelectRoute rejects an
//     alias with neither a fixed price nor a positive reservation estimate
//     with ErrAliasNotPriced before a batch runs, because that is static
//     model_aliases configuration checked once per batch.
//   - The last two rows are RUNTIME properties of one response, which
//     SelectRoute cannot see. They are unreachable only because hive-auto is
//     the sole alias carrying supports_batch and it is upstream_actual, so no
//     fixed-price alias reaches this branch at all today. Give a fixed-price
//     alias a batch route and these go live.
func TestDefaultCreditPolicy_RefusesAnUnpriceableLine(t *testing.T) {
	zeroEstimate := int64(0)
	for _, tc := range []struct {
		name    string
		pricing catalog.CatalogPricing
		usage   *Usage
	}{
		{"no pricing mode and no price", catalog.CatalogPricing{}, &Usage{PromptTokens: 8, CompletionTokens: 5}},
		{"fixed mode with both sides zero", catalog.FixedPricing(0, 0), &Usage{PromptTokens: 8, CompletionTokens: 5}},
		{"upstream_actual with no hold", catalog.CatalogPricing{PricingMode: catalog.PricingModeUpstreamActual}, &Usage{PromptTokens: 8, CompletionTokens: 5}},
		{"upstream_actual with a zero hold", catalog.CatalogPricing{PricingMode: catalog.PricingModeUpstreamActual, ReservationEstimateCredits: &zeroEstimate}, &Usage{PromptTokens: 8, CompletionTokens: 5}},
		{"fixed price, no usage reported", testFixedPricing(), nil},
		// Components both zero is the same "nothing to price" case. A total of
		// 31 is deliberately present and deliberately ignored: it is the very
		// figure #1473 removes, so it must not become an escape hatch here.
		{"fixed price, zero components with a nonzero total", testFixedPricing(), &Usage{TotalTokens: 31}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DefaultCreditPolicy{}.Credits(tc.pricing, tc.usage, body(t, "gen-1", "0.0001", 8, 5))
			if err == nil {
				t.Fatalf("settled %+v, want a refusal: charging anything here would be an invented figure", got)
			}
			// The returned LinePrice is asserted EMPTY, not merely zero-credit:
			// a refusal must not hand the caller a partially-populated
			// settlement that a later change could start charging from.
			if got != (LinePrice{}) {
				t.Fatalf("refused line carried a settlement %+v, want the zero value", got)
			}
		})
	}
}

// TestSettlementConstantsMatchTheMoneyPath pins the constants this package had
// to restate because Go's internal-package visibility makes the originals
// unimportable from control-plane. If a shared packages/pricing is ever
// extracted, this test is what proves the copies never drifted in the meantime.
func TestSettlementConstantsMatchTheMoneyPath(t *testing.T) {
	if creditsPerUSD != payments.CreditsPerUSD {
		t.Fatalf("creditsPerUSD = %d, want payments.CreditsPerUSD (%d): the D-046 rescale moved one and not the other",
			creditsPerUSD, payments.CreditsPerUSD)
	}
	// 7/5 is inference.MarginNumerator / MarginDenominator, the 1.4 margin
	// expressed exactly as a rational so no float64 touches a charge.
	if marginNumerator != 7 || marginDenominator != 5 {
		t.Fatalf("margin = %d/%d, want 7/5", marginNumerator, marginDenominator)
	}
	// D-031: prices are stored per million metered units.
	if creditsPerMillion != 1_000_000 {
		t.Fatalf("creditsPerMillion = %d, want 1000000", creditsPerMillion)
	}
	// inference.maxChargeableCredits, 10 USD, restated at the same value.
	if maxChargeableCredits != 10*payments.CreditsPerUSD {
		t.Fatalf("maxChargeableCredits = %d, want %d (10 USD)", maxChargeableCredits, 10*payments.CreditsPerUSD)
	}
}

// TestDispatcher_SettlesAtTheAliasPriceEndToEnd runs the whole live shape
// through Dispatch rather than the policy alone: hive-auto's upstream_actual
// pricing, a provider-reported cost on the raw body, and a thinking-capable
// usage block whose total exceeds its components.
//
// It is the end-to-end half of issue #1473's acceptance 4: the charge and its
// provenance are compared in ONE assertion on ONE request, so a fix that
// restores the amount while emptying the generation id still fails here.
func TestDispatcher_SettlesAtTheAliasPriceEndToEnd(t *testing.T) {
	raw := body(t, "gen-live-1", "0.0001", 8, 5)
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			return raw, &Usage{PromptTokens: 8, CompletionTokens: 5, TotalTokens: 31}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "hive-auto", ""),
		Alias:        "hive-auto",
		LiteLLMModel: "openrouter/openrouter/auto",
		Pricing:      upstreamActual(),
	})
	if res.Error != nil {
		t.Fatalf("unexpected failed line: %+v", res.Error)
	}
	want := LinePrice{Credits: 140_000, Confirmed: true, Reason: "upstream_cost", GenerationID: "gen-live-1"}
	if res.Settlement != want {
		t.Fatalf("settled %+v, want %+v", res.Settlement, want)
	}
	// The cost the charge came from must not survive into the customer's
	// output.jsonl. Asserted on the serialized body rather than a struct,
	// because that is the only thing that can see a field the sanitizer failed
	// to strip.
	if strings.Contains(string(res.Output.Response.Body), "cost") {
		t.Fatalf("provider-reported cost reached the customer's batch output: %s", res.Output.Response.Body)
	}
}
