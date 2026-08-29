package inference

import (
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #1372: selecting Hive Auto in the chat model picker made every
// subsequent turn fail with a quota refusal while the same screen showed
// 0.455 USD of credit remaining. The alias holds credit up front, and the hold
// it took was the price of the LARGEST request the bounds allow, not of the
// request in hand, so no account below 2.00 USD could use it at all.
//
// The two properties these tests hold apart point in opposite directions and
// both have to survive every future change to the sizing:
//
//	ADMIT  an account that can pay for the request it actually sent
//	REFUSE an account that cannot pay for the request it actually sent
//
// Only the second one is enforced anywhere else (control-plane's enforcePolicy
// compares the hold against the balance), so a change that quietly shrinks the
// hold too far shows up here or nowhere.

// qaBalanceCredits is the balance the defect was reported on, in the current
// credit unit: 0.455 USD at 1 USD = 1e9 credits (D-046).
const qaBalanceCredits = 455_000_000

// envelopeHoldCredits is the catalog's whole-envelope hold for hive-auto,
// reservation_estimate_credits in 20260824_02_free_pool_router.sql.
const envelopeHoldCredits = 2_000_000_000

// boundedChatBody runs a body through the real pre-dispatch bound, which is
// where a variable-price request gets the completion ceiling that the hold is
// then sized against. Sizing a hold from an unbounded body would be a number
// with nothing behind it, so the tests never do it either.
func boundedChatBody(t *testing.T, route SelectRouteResult, body string) []byte {
	t.Helper()
	w := httptest.NewRecorder()
	bounded, ok := EnforceVariablePriceBounds(w, route, EndpointChatCompletions, "hive-auto", []byte(body))
	if !ok {
		t.Fatalf("fixture was refused by the bounds, so it cannot be used to size a hold: %s", w.Body.String())
	}
	return bounded
}

// A chat turn of the size a person actually sends. Deliberately not a minimal
// `{}`: the point is that an ordinary turn, not a degenerate one, now fits.
const ordinaryChatTurn = `{"model":"hive-auto","messages":[` +
	`{"role":"system","content":"You are a helpful assistant for a developer platform."},` +
	`{"role":"user","content":"Summarise what an API gateway does, in two sentences."},` +
	`{"role":"assistant","content":"It sits in front of one or more upstream services and gives callers a single, stable, authenticated entry point."},` +
	`{"role":"user","content":"And what does it add on top of a plain reverse proxy?"}` +
	`],"stream":true}`

func TestASolventAccountIsNotRefusedForAnEnvelopeItIsNotUsing(t *testing.T) {
	route := SelectRouteResult{
		Pricing:   UpstreamActualPricing(envelopeHoldCredits),
		PriceUnit: PriceUnitTokens,
	}
	body := boundedChatBody(t, route, ordinaryChatTurn)

	// The defect, stated as arithmetic: the whole-envelope hold is more than
	// four times this account's entire balance, so control-plane refused every
	// turn on it before a token was generated.
	if envelopeHoldCredits <= qaBalanceCredits {
		t.Fatalf("fixture no longer reproduces the defect: envelope hold %d is within a %d balance",
			envelopeHoldCredits, qaBalanceCredits)
	}

	hold := ReservationCredits(route, DefaultHoldText, EndpointChatCompletions, body)
	if hold > qaBalanceCredits {
		t.Fatalf("an ordinary chat turn still holds %d credits against a %d balance, so Hive Auto is still refused on it. "+
			"The hold has to be sized from this request, not from the largest request the bounds allow.",
			hold, qaBalanceCredits)
	}
	t.Logf("ordinary turn holds %d credits (%.4f USD) against a %d balance; the envelope hold was %d",
		hold, float64(hold)/float64(CreditsPerUSD), qaBalanceCredits, envelopeHoldCredits)
}

func TestAnAccountThatCannotAffordTheRequestIsStillRefused(t *testing.T) {
	route := SelectRouteResult{
		Pricing:   UpstreamActualPricing(envelopeHoldCredits),
		PriceUnit: PriceUnitTokens,
	}

	// A request at the size cap really can cost more than this balance, so the
	// hold must still exceed it. This is the property the fix must not trade
	// away: under-reserving does not refuse anyone, it just lets a request cost
	// more than the account was ever checked against.
	atTheCap := `{"model":"hive-auto","messages":[{"role":"user","content":"` +
		strings.Repeat("x", VariablePriceMaxRequestBytes-200) + `"}]}`
	body := boundedChatBody(t, route, atTheCap)

	hold := ReservationCredits(route, DefaultHoldText, EndpointChatCompletions, body)
	if hold <= qaBalanceCredits {
		t.Fatalf("a request at the size cap holds only %d credits, which a %d balance covers. "+
			"That request can cost far more than that, so the hold is no longer a solvency gate.",
			hold, qaBalanceCredits)
	}

	// And nothing shrinks below the endpoint's own floor, so an account with
	// almost nothing on it is refused whatever it sends.
	tiny := ReservationCredits(route, DefaultHoldText, EndpointChatCompletions,
		boundedChatBody(t, route, `{"model":"hive-auto","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`))
	if tiny < DefaultHoldText {
		t.Fatalf("hold %d fell below the endpoint floor %d", tiny, DefaultHoldText)
	}
}

// TestTheSizedHoldStillCoversWhatTheRequestCanCost is the coverage proof, per
// request rather than per envelope. It re-derives the bound from the RATE
// CEILING IN THE LITELLM CONFIG rather than from the Go constants the
// implementation uses, so a drift between those two fails here as well as in
// TestVariablePriceCeilingsMatchTheLiteLLMConfig.
func TestTheSizedHoldStillCoversWhatTheRequestCanCost(t *testing.T) {
	promptCeiling, completionCeiling := readMaxPriceCeiling(t, "route-openrouter-auto-live")
	route := SelectRouteResult{
		Pricing:   UpstreamActualPricing(envelopeHoldCredits),
		PriceUnit: PriceUnitTokens,
	}

	cases := []struct {
		name       string
		body       string
		completion int64
	}{
		{"no ceiling set by the caller", ordinaryChatTurn, VariablePriceMaxCompletionTokens},
		{"caller set a small ceiling", `{"model":"hive-auto","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`, 100},
		{"caller set a ceiling above ours", `{"model":"hive-auto","messages":[{"role":"user","content":"hi"}],"max_tokens":900000}`, VariablePriceMaxCompletionTokens},
		{"at the size cap", `{"model":"hive-auto","messages":[{"role":"user","content":"` + strings.Repeat("x", VariablePriceMaxRequestBytes-200) + `"}]}`, VariablePriceMaxCompletionTokens},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := boundedChatBody(t, route, tc.body)
			hold := ReservationCredits(route, DefaultHoldText, EndpointChatCompletions, body)

			// len(body) bytes is a rigorous upper bound on prompt tokens: a
			// token is never fewer than one UTF-8 byte, and the body also
			// carries JSON structure that is not prompt text at all.
			worst := new(big.Rat).Add(
				new(big.Rat).Mul(
					new(big.Rat).SetInt64(int64(len(body))),
					new(big.Rat).Quo(promptCeiling, new(big.Rat).SetInt64(1_000_000)),
				),
				new(big.Rat).Mul(
					new(big.Rat).SetInt64(tc.completion),
					new(big.Rat).Quo(completionCeiling, new(big.Rat).SetInt64(1_000_000)),
				),
			)
			worstCredits, err := CreditsForUpstreamCost(worst)
			if err != nil {
				t.Fatalf("could not price the worst case for this request: %v", err)
			}

			if hold < worstCredits {
				t.Fatalf("the hold does NOT cover this request: worst case is %d credits, hold is %d. "+
					"A hold smaller than what the request can cost lets the charge land outside what the account was checked against.",
					worstCredits, hold)
			}
			if hold > envelopeHoldCredits {
				t.Fatalf("hold %d exceeds the catalog's whole-envelope hold %d, which is meant to be the ceiling on this arithmetic",
					hold, envelopeHoldCredits)
			}
			t.Logf("worst %d credits, hold %d", worstCredits, hold)
		})
	}
}

// TestVariablePriceCeilingsMatchTheLiteLLMConfig keeps the rate half of the
// bound honest across the two files that have to agree on it. The request path
// cannot read deploy/litellm/config.yaml, so the numbers are restated as Go
// constants; this is what stops the restatement drifting from the thing being
// restated.
//
// Both variable-price routes are checked. route-openrouter-auto-live is the one
// hive-auto serves, and it is the reason this guard names routes explicitly
// rather than checking whichever one it finds: the live config had that route
// with no max_price at all, generated from provider_routes by a config sync
// that owns only model, api_base and api_key, and nothing failed.
func TestVariablePriceCeilingsMatchTheLiteLLMConfig(t *testing.T) {
	for _, route := range []string{"route-openrouter-auto-beta", "route-openrouter-auto-live"} {
		t.Run(route, func(t *testing.T) {
			prompt, completion := readMaxPriceCeiling(t, route)
			if prompt.Cmp(new(big.Rat).SetInt64(VariablePricePromptCeilingUSD)) != 0 {
				t.Errorf("prompt ceiling is %s in deploy/litellm/config.yaml but %d in Go",
					prompt.RatString(), VariablePricePromptCeilingUSD)
			}
			if completion.Cmp(new(big.Rat).SetInt64(VariablePriceCompletionCeilingUSD)) != 0 {
				t.Errorf("completion ceiling is %s in deploy/litellm/config.yaml but %d in Go",
					completion.RatString(), VariablePriceCompletionCeilingUSD)
			}
		})
	}
}

// A fixed-price alias has no reservation estimate at all, so none of this
// applies to it and its hold is the flat endpoint figure, unchanged.
func TestAFixedPriceAliasKeepsTheFlatEndpointHold(t *testing.T) {
	route := SelectRouteResult{Pricing: FixedPricing(105_000_000, 420_000_000), PriceUnit: PriceUnitTokens}
	if got := ReservationCredits(route, DefaultHoldText, EndpointChatCompletions, []byte(ordinaryChatTurn)); got != DefaultHoldText {
		t.Errorf("fixed-price hold = %d, want the endpoint default %d", got, DefaultHoldText)
	}
	if got := ReservationCredits(route, DefaultHoldEmbeddings, EndpointEmbeddings, []byte(`{"input":"hi"}`)); got != DefaultHoldEmbeddings {
		t.Errorf("embeddings hold = %d, want the endpoint default %d", got, DefaultHoldEmbeddings)
	}
}
