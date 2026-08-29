package inference

import (
	"errors"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Tests for billing a variable-price alias at the cost the upstream reported.
//
// The thing being defended here is narrow and specific: a request must never
// settle at zero because the cost lookup failed. Every assertion below is
// written so that it FAILS if that guard is removed, rather than merely
// passing while the guard happens to exist. Where a test could be satisfied by
// the very bug it targets, the assertion is on an exact magnitude rather than
// on "not zero", because "not zero" is satisfied by the 1-credit floor and the
// floor is not the guard.

// --- ParseUpstreamCost: absent, zero, negative and unreadable are DIFFERENT --
//
// Collapsing these into one failure is the specific mistake this package
// exists to avoid: a confident zero and a missing field have different causes,
// and only one of them is even arguably legitimate.

func TestParseUpstreamCostDistinguishesEveryFailureShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{
			name: "no usage object at all: the shape a proxy that dropped the field produces",
			body: `{"id":"gen-abc","choices":[]}`,
			want: ErrUpstreamCostAbsent,
		},
		{
			name: "usage present but no cost key",
			body: `{"id":"gen-abc","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			want: ErrUpstreamCostAbsent,
		},
		{
			name: "cost explicitly null",
			body: `{"id":"gen-abc","usage":{"prompt_tokens":10,"completion_tokens":5,"cost":null}}`,
			want: ErrUpstreamCostAbsent,
		},
		{
			name: "cost is not a number",
			body: `{"id":"gen-abc","usage":{"prompt_tokens":10,"completion_tokens":5,"cost":"free"}}`,
			want: ErrUpstreamCostUnparseable,
		},
		{
			name: "body is not JSON",
			body: `<html>502 Bad Gateway</html>`,
			want: ErrUpstreamCostUnparseable,
		},
		{
			name: "negative cost must never reduce a charge",
			body: `{"id":"gen-abc","usage":{"prompt_tokens":10,"completion_tokens":5,"cost":-0.5}}`,
			want: ErrUpstreamCostNegative,
		},
		{
			// The July shape. Tokens were consumed, so work was done, and a
			// cost of exactly zero alongside them is not credible.
			name: "confident zero alongside real tokens is its own error, not absent",
			body: `{"id":"gen-abc","usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":0}}`,
			want: ErrUpstreamCostZero,
		},
		{
			// Zero with no tokens is genuinely "nothing happened", which the
			// caller releases rather than charges.
			name: "zero with no tokens is absent, not the suspicious zero",
			body: `{"id":"gen-abc","usage":{"prompt_tokens":0,"completion_tokens":0,"cost":0}}`,
			want: ErrUpstreamCostAbsent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseUpstreamCost([]byte(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			// Guards the distinction itself: if every failure collapsed to one
			// sentinel this would pass above and fail here.
			for _, other := range []error{
				ErrUpstreamCostAbsent, ErrUpstreamCostUnparseable,
				ErrUpstreamCostNegative, ErrUpstreamCostZero,
			} {
				if other != tc.want && errors.Is(err, other) {
					t.Fatalf("error %v also matches %v; the failure shapes must stay distinguishable", err, other)
				}
			}
		})
	}
}

func TestParseUpstreamCostReadsCostExactlyAndCapturesAuditHandles(t *testing.T) {
	// A deliberately long decimal: a float64 round-trip of this value does not
	// compare equal to the exact rational, so this fails if the parser ever
	// goes through a float.
	body := `{"id":"gen-1755900000-XYZ","provider":"Anthropic",` +
		`"usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":0.000000123456789}}`

	charge, err := ParseUpstreamCost([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want, _ := new(big.Rat).SetString("0.000000123456789")
	if charge.CostUSD.Cmp(want) != 0 {
		t.Fatalf("cost read inexactly: want %s, got %s", want.RatString(), charge.CostUSD.RatString())
	}
	// The generation id is the audit handle: it is what lets anyone recover
	// which model the router actually chose, so losing it silently would make
	// a charge unauditable.
	if charge.GenerationID != "gen-1755900000-XYZ" {
		t.Fatalf("generation id not captured, got %q", charge.GenerationID)
	}
}

// --- CreditsForUpstreamCost: the charge magnitude must be RIGHT ------------
//
// Every expectation is derived longhand from a known upstream cost. Asserting
// "greater than zero" would pass on the 1-credit floor, which is exactly the
// near-free outcome a broken conversion produces, so no assertion here is of
// that shape.

func TestCreditsForUpstreamCostMagnitude(t *testing.T) {
	tests := []struct {
		name string
		cost string
		want int64
	}{
		{
			// The figure measured from a real OpenRouter usage block.
			// Exact product: 0.0123456 x 1.4 x 1e9 = 17,283,840 with no
			// remainder, so no rounding applies. Same real money as the
			// pre-rescale unit's 1728 old credits.
			name: "measured cost, exact product",
			cost: "0.0123456",
			want: 17_283_840,
		},
		{
			// 1.00 x 1.4 x 1e9 = 1,400,000,000 exactly. If the margin were
			// dropped this reads 1e9; if it were applied twice, 1.96e9.
			name: "one dollar is exactly the margin times the credit rate",
			cost: "1.00",
			want: 1_400_000_000,
		},
		{
			// 0.0000000025 x 1.4e9 = 3.5 exactly: the half-up boundary.
			// Round half DOWN gives 3, truncation gives 3, only half-up
			// gives 4. (Pre-rescale this boundary sat at 0.000025.)
			name: "exact half rounds up",
			cost: "0.0000000025",
			want: 4,
		},
		{
			// 0.123456789 x 1.4e9 = 172,839,504.6 -> 172,839,505
			name: "long decimal rounds up",
			cost: "0.123456789",
			want: 172_839_505,
		},
		{
			// Exact product this time: 0.0000001 x 1.4e9 = 140 credits with
			// no rounding involved (the pre-rescale unit floored its 0.014-credit
			// product to 1; that comparison is era trivia, not arithmetic).
			name: "a tiny but real cost floors at one credit, never zero",
			cost: "0.0000001",
			want: 140,
		},
		{
			// 1e-12 USD x 1.4e9 = 0.0014 floors to 1: at the finer new unit
			// the floor still catches costs far below one credit. This case
			// is a FLOORING case, not a scaled one: the product is far below
			// one credit in both units.
			name: "a sub-floor cost still charges one credit",
			cost: "0.000000000001",
			want: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cost, ok := new(big.Rat).SetString(tc.cost)
			if !ok {
				t.Fatalf("bad test fixture %q", tc.cost)
			}
			got, err := CreditsForUpstreamCost(cost)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("cost %s: want %d credits, got %d", tc.cost, tc.want, got)
			}
		})
	}
}

func TestCreditsForUpstreamCostRefusesNonPositive(t *testing.T) {
	if _, err := CreditsForUpstreamCost(nil); !errors.Is(err, ErrUpstreamCostAbsent) {
		t.Fatalf("nil cost: expected absent, got %v", err)
	}
	if _, err := CreditsForUpstreamCost(new(big.Rat)); !errors.Is(err, ErrUpstreamCostZero) {
		t.Fatalf("zero cost: expected zero error, got %v", err)
	}
	neg, _ := new(big.Rat).SetString("-1")
	if _, err := CreditsForUpstreamCost(neg); !errors.Is(err, ErrUpstreamCostNegative) {
		t.Fatalf("negative cost: expected negative error, got %v", err)
	}
}

// --- The free-serve guard --------------------------------------------------
//
// The three failure modes the brief calls out, as three separate cases:
// the lookup returns null, the lookup errors (an unreadable body), and the
// lookup never happens at all because the stream ended before the terminal
// usage frame arrived. That last one is what a timeout looks like on this
// path: the cost is carried IN BAND on the response, so there is no separate
// call to time out, and a timed-out or aborted stream simply leaves no bytes
// to read. Testing it as "no bytes" rather than inventing a network timeout is
// the honest mapping.

func TestFreeServeGuardNeverSettlesAtZero(t *testing.T) {
	const held = 200_000

	tests := []struct {
		name       string
		rawUsage   string
		wantReason string
	}{
		{
			name:       "cost returns null",
			rawUsage:   `{"id":"gen-1","usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":null}}`,
			wantReason: "upstream_cost_absent",
		},
		{
			name:       "cost lookup errors on an unreadable body",
			rawUsage:   `{"id":"gen-1","usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":"nope"}}`,
			wantReason: "upstream_cost_unparseable",
		},
		{
			// Absence, not a parse failure. json.Unmarshal reports empty input
			// as a syntax error, which used to file a stream that ended before
			// its usage frame under `unparseable` and lose the very
			// distinction this package is built around.
			name:       "the lookup never happened: stream ended before the usage frame",
			rawUsage:   ``,
			wantReason: "upstream_cost_absent",
		},
		{
			name:       "a confident zero is refused, not billed as free",
			rawUsage:   `{"id":"gen-1","usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":0}}`,
			wantReason: "upstream_cost_zero",
		},
		{
			name:       "a negative cost cannot reduce the charge",
			rawUsage:   `{"id":"gen-1","usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":-5}}`,
			wantReason: "upstream_cost_negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			settled := UpstreamActualSettlement(
				[]byte(tc.rawUsage), held, true, 1000, 500, "some answer text")
			credits, confirmed, delivered, reason := settled.Credits, settled.Confirmed, settled.Delivered, settled.Reason

			if !delivered {
				t.Fatal("work was delivered; refusing to charge for it is the free-serve bug itself")
			}
			// The load-bearing assertion. Zero here IS the July incident.
			if credits == 0 {
				t.Fatal("settled at ZERO after a failed cost lookup: this is the free-serve bug")
			}
			// Exact, not "greater than zero": a 1-credit floor would satisfy
			// non-zero while still giving the work away.
			if credits != held {
				t.Fatalf("expected the full hold %d to be charged, got %d", held, credits)
			}
			if confirmed {
				t.Fatal("a charge derived from a FAILED cost lookup must never be flagged as measured truth")
			}
			if reason != tc.wantReason {
				t.Fatalf("expected reason %q, got %q", tc.wantReason, reason)
			}
		})
	}
}

func TestUpstreamActualSettlementChargesTheReportedCost(t *testing.T) {
	raw := `{"id":"gen-1","provider":"Anthropic","usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":0.0123456}}`

	settled := UpstreamActualSettlement([]byte(raw), 200_000, true, 1000, 500, "answer")
	credits, confirmed, delivered, reason := settled.Credits, settled.Confirmed, settled.Delivered, settled.Reason

	// The audit handle must survive settlement, not just parsing: this is what
	// recovers the model the router chose, and the response no longer names it.
	if settled.GenerationID != "gen-1" {
		t.Errorf("generation id not carried through settlement, got %q", settled.GenerationID)
	}

	if !delivered || !confirmed {
		t.Fatalf("expected a confirmed delivered settlement, got delivered=%v confirmed=%v", delivered, confirmed)
	}
	// 0.0123456 x 1.4 x 100000 = 1728.384 -> 1728. Compared against the KNOWN
	// upstream cost, not against the hold and not against zero. If settlement
	// ever fell back to the hold on a readable cost this reads 200000.
	if credits != 17_283_840 {
		t.Fatalf("expected 17283840 credits for a reported cost of 0.0123456, got %d", credits)
	}
	if reason != "upstream_cost" {
		t.Fatalf("expected reason upstream_cost, got %q", reason)
	}
}

func TestUpstreamActualSettlementReleasesWhenNothingWasDelivered(t *testing.T) {
	// The ONLY path allowed to produce a zero charge: no tokens, no content.
	settled := UpstreamActualSettlement(nil, 200_000, false, 0, 0, "")
	credits, confirmed, delivered, reason := settled.Credits, settled.Confirmed, settled.Delivered, settled.Reason
	if delivered || confirmed || credits != 0 {
		t.Fatalf("nothing delivered should release: got credits=%d confirmed=%v delivered=%v", credits, confirmed, delivered)
	}
	if reason != "nothing_delivered" {
		t.Fatalf("expected reason nothing_delivered, got %q", reason)
	}
}

// --- The reservation cannot under-reserve ----------------------------------

func TestReservationCreditsCannotUnderReserve(t *testing.T) {
	const endpointDefault = DefaultHoldText

	fixed := SelectRouteResult{Pricing: FixedPricing(10_500, 42_000)}
	if got := ReservationCredits(fixed, endpointDefault, EndpointChatCompletions, []byte(`{"messages":[]}`)); got != endpointDefault {
		t.Fatalf("a fixed-price alias must keep the endpoint default, got %d", got)
	}

	// The real case: the catalog row for openrouter-auto holds 2e9 (its
	// $2.00-equivalent after the 2026-08-23 rescale), twenty times the flat
	// default. If the catalog figure were ignored this reads the flat
	// default, and a router request that resolved to an expensive model would
	// be held against a hold far too small to cover it.
	//
	// The body here is over VariablePriceMaxRequestBytes, so no per-request
	// bound can be computed for it and the catalog figure is what stands. That
	// is the fallback direction on purpose: a request nobody could size falls
	// back to the whole-envelope hold, never to something smaller. The
	// per-request sizing that applies to an ordinary request instead is in
	// reservation_sizing_test.go (issue #1372).
	variable := SelectRouteResult{Pricing: UpstreamActualPricing(2_000_000_000)}
	unsizable := []byte(`{"messages":"` + strings.Repeat("x", VariablePriceMaxRequestBytes) + `"}`)
	if got := ReservationCredits(variable, endpointDefault, EndpointChatCompletions, unsizable); got != 2_000_000_000 {
		t.Fatalf("a variable-price alias must fall back to its catalog figure 2000000000, got %d", got)
	}

	// The endpoint default is a FLOOR, never a ceiling: a catalog row that
	// somehow carried a smaller hold must not lower the hold below what the
	// endpoint already reserves.
	tooSmall := SelectRouteResult{Pricing: UpstreamActualPricing(5)}
	if got := ReservationCredits(tooSmall, endpointDefault, EndpointChatCompletions, []byte(`{"messages":[]}`)); got != endpointDefault {
		t.Fatalf("the endpoint default must floor the hold, got %d", got)
	}
}

// --- A variable-price alias must never be priced from the catalog columns ---

func TestCanPriceTokensAcceptsVariablePricingAndStillRefusesUnpriced(t *testing.T) {
	variable := SelectRouteResult{Pricing: UpstreamActualPricing(200_000), PriceUnit: PriceUnitTokens}
	if !CanPriceTokens(variable) {
		t.Fatal("a variable-price alias is priceable by actual cost and must not be refused up front")
	}

	// Its price columns are nil. If anything ever reads them as a fixed price
	// it would price every token at zero, so the mode has to be what decides.
	if variable.Pricing.InputPriceCredits != nil || variable.Pricing.OutputPriceCredits != nil {
		t.Fatal("a variable-price alias must carry no fixed price at all")
	}

	// A row with neither a mode nor a price is still refused: this is the
	// no-cost-basis case and it must not become priceable by accident.
	unpriced := SelectRouteResult{PriceUnit: PriceUnitTokens}
	if CanPriceTokens(unpriced) {
		t.Fatal("an alias with no price and no pricing mode must stay unpriceable")
	}

	// Variable pricing does not override the unit check: a token endpoint
	// still cannot meter a per-second alias.
	wrongUnit := SelectRouteResult{Pricing: UpstreamActualPricing(200_000), PriceUnit: "seconds"}
	if CanPriceTokens(wrongUnit) {
		t.Fatal("a token endpoint must not accept a per-second alias even in variable mode")
	}
}

// --- Cross-module constant guard -------------------------------------------

// CreditsPerUSD is duplicated in this package because payments lives under
// control-plane's internal/ tree in another module and Go's visibility rules
// make it unimportable. A duplicated money constant that can drift silently is
// worse than no constant, so this reads the real declaration off disk and
// fails if the two ever disagree.
func TestCreditsPerUSDMatchesPaymentsPackage(t *testing.T) {
	const src = "../../../control-plane/internal/payments/types.go"
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("cannot read %s, which this guard depends on: %v", src, err)
	}

	m := regexp.MustCompile(`CreditsPerUSD\s+int64\s*=\s*([0-9_]+)`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("could not find the CreditsPerUSD declaration in %s; if it moved, move this guard with it", src)
	}
	// Compared against THIS package's constant, not against a literal. A
	// literal here would keep passing after someone edited the constant, which
	// is the exact drift the guard exists to catch.
	declared := strings.ReplaceAll(string(m[1]), "_", "")
	if declared != strconv.FormatInt(CreditsPerUSD, 10) {
		t.Fatalf("payments.CreditsPerUSD is now %s but this package uses %d; the two must agree",
			declared, CreditsPerUSD)
	}
}
