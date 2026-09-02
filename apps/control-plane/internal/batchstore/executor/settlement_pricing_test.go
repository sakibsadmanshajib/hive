package executor

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
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
// provider reported for that specific generation, at 1e9 credits per USD and
// with no margin factor (D-064).
func TestDefaultCreditPolicy_UpstreamActualSettlesFromReportedCost(t *testing.T) {
	for _, tc := range []struct {
		name string
		cost string
		want int64
	}{
		// 0.0001 USD x 1e9 = 100,000 credits, exact.
		{"ordinary cost", "0.0001", 100_000},
		// 0.0000000001 USD x 1e9 = 0.1, which rounds to zero and is then
		// floored at one credit: a line that cost real money is never settled
		// free.
		{"sub-credit cost floors at one", "0.0000000001", 1},
		// Just inside the 10 USD per-line ceiling: 9.9999999994 x 1e9 is
		// 9,999,999,999.4, which rounds half down to 9,999,999,999 and settles
		// rather than being refused.
		{"just inside the ceiling", "9.9999999994", 9_999_999_999},
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
		// Scientific notation is valid JSON and fits the byte cap, so the cap
		// alone does not bound the parse work it was written to bound. 56 nines
		// with e999999 is 63 bytes and expands to a roughly 3.3 million bit
		// integer. Rejected on shape instead: a USD cost has no legitimate
		// reason to carry an exponent.
		{"exponent literal inside the byte cap", body(t, "gen-1", strings.Repeat("9", 56)+"e999999", 8, 5), "upstream_cost_unparseable"},
		{"ordinary exponent literal", body(t, "gen-1", "1e5", 8, 5), "upstream_cost_unparseable"},
		// 100 USD = 100,000,000,000 credits, past the 10 USD per-line
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
	want := LinePrice{Credits: 100_000, Confirmed: true, Reason: "upstream_cost", GenerationID: "gen-live-1"}
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

// TestDefaultCreditPolicy_RefusesRatherThanClampsAnImplausibleCharge is the
// guard for the review finding that outranked every other one on this PR.
//
// creditsForTokens used to CLAMP to maxChargeableCredits when the computed
// charge did not fit an int64. That settles a failed computation as a real
// charge, and the clamped line is byte-for-byte indistinguishable from a
// genuine ten dollar line: same amount, same confirmed flag, same
// catalog_price reason. Nobody could ever find those lines afterwards to audit
// or refund them, which is what makes an unauditable wrong charge worse than a
// loud one.
//
// Both pricing paths now refuse on the same condition, matching
// upstream_cost.go, which is the only other place this ceiling exists and
// which has always refused rather than substituted a number.
//
// The 1,000,000 rate is not invented for this test: it is testFixedPricing,
// the fixture the rest of this file already prices with, which is exactly why
// a path the suite never covered was reachable by the suite's own numbers.
func TestDefaultCreditPolicy_RefusesRatherThanClampsAnImplausibleCharge(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		inRate                   int64
		promptTokens, compTokens int64
	}{
		// MaxInt64 tokens x 1e9 credits per million is 9.2e21 credits, past
		// what an int64 can hold at all.
		{"charge overflows int64", 1_000_000_000, math.MaxInt64, 0},
		// 1e9 tokens x 1e9 credits per million is 1e12 credits, 1000 USD. It
		// fits an int64 comfortably, so no overflow is involved: this is
		// purely the per-line ceiling, and without it the charge settles
		// confirmed with no flag of any kind.
		{"charge exceeds the per-line ceiling", 1_000_000_000, 1_000_000_000, 0},
		// The suite's own fixture rate, reached from the completion side.
		{"the suite's own fixture rate reaches it", 1_000_000, 0, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DefaultCreditPolicy{}.Credits(catalog.FixedPricing(tc.inRate, tc.inRate),
				&Usage{PromptTokens: tc.promptTokens, CompletionTokens: tc.compTokens}, nil)
			if err == nil {
				t.Fatalf("settled %+v, want a refusal: a charge that cannot be computed honestly "+
					"must never be replaced by the ceiling, which is indistinguishable from a real charge at it", got)
			}
			// Asserted EMPTY rather than merely under the ceiling: the whole
			// defect was handing back a plausible-looking settlement, so a
			// refusal must carry no chargeable figure at all.
			if got != (LinePrice{}) {
				t.Fatalf("refused line carried a settlement %+v, want the zero value", got)
			}
		})
	}
}

// TestRoundHalfUp_RefusesNegative pins the behaviour its doc comment claims.
// QuoRem truncates toward zero, so the half-up bump rounds a negative AWAY
// from zero, meaning the documented behaviour was simply false for negatives.
// Both callers guarantee non-negative input today; this refuses anyway,
// because "currently dead" is how the missing ceiling above reached review.
func TestRoundHalfUp_RefusesNegative(t *testing.T) {
	if _, ok := roundHalfUp(big.NewRat(-3, 2)); ok {
		t.Fatalf("roundHalfUp accepted a negative rational, which it cannot round as documented")
	}
	if got, ok := roundHalfUp(big.NewRat(3, 2)); !ok || got != 2 {
		t.Fatalf("roundHalfUp(3/2) = %d, %v; want 2, true", got, ok)
	}
}

// TestExplainsReportedTotal_HandlesHostileCounts covers the log-label helper.
// Every field it reads is upstream controlled. It has no charge impact, which
// is why it stays a label, but it must not read a violated identity as
// satisfied through int64 wraparound, and a negative total is nonsense rather
// than an absent one.
func TestExplainsReportedTotal_HandlesHostileCounts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *Usage
		want  bool
	}{
		{"nil usage has no identity to violate", nil, true},
		{"absent total has no identity to violate", &Usage{PromptTokens: 8, CompletionTokens: 5}, true},
		{"negative total is nonsense, not absence", &Usage{PromptTokens: 8, CompletionTokens: 5, TotalTokens: -1}, false},
		// The row that actually separates the implementations. Plain int64
		// addition of MaxInt64 and 1 wraps to exactly MinInt64, which then
		// compares EQUAL to this reported total, so the old arithmetic would
		// call a nonsense shape explained. big.Int does not wrap, and the
		// negative-total check refuses it first. A row whose verdict is the
		// same under both implementations proves nothing, which is why this
		// one is chosen over a merely large pair.
		{"components wrap to exactly the reported total",
			&Usage{PromptTokens: math.MaxInt64, CompletionTokens: 1, TotalTokens: math.MinInt64}, false},
		{"alongside convention", &Usage{PromptTokens: 4, CompletionTokens: 1, ReasoningTokens: 26, TotalTokens: 31}, true},
		{"inside convention", &Usage{PromptTokens: 4, CompletionTokens: 27, ReasoningTokens: 26, TotalTokens: 31}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := explainsReportedTotal(tc.usage); got != tc.want {
				t.Fatalf("explainsReportedTotal = %v, want %v", got, tc.want)
			}
		})
	}
}
