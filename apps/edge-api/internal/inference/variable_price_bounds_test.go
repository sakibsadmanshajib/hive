package inference

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The request bounds exist because provider.max_price caps the RATE and not the
// size of a request. Without them a single large-context call against a router
// alias can settle several times past its hold, which is the reservation
// under-reserving in the only way that actually matters.

func TestEnforceVariablePriceBounds_RefusesAnOversizeRequest(t *testing.T) {
	route := SelectRouteResult{Pricing: UpstreamActualPricing(200_000), PriceUnit: PriceUnitTokens}

	// One byte over. The boundary is the whole point, so it is tested at the
	// boundary rather than with an obviously huge body.
	body := []byte(`{"messages":"` + strings.Repeat("x", VariablePriceMaxRequestBytes) + `"}`)
	if len(body) <= VariablePriceMaxRequestBytes {
		t.Fatalf("fixture is not actually oversize: %d", len(body))
	}

	w := httptest.NewRecorder()
	got, ok := EnforceVariablePriceBounds(w, route, EndpointChatCompletions, "openrouter-auto", body)
	if ok {
		t.Fatal("an oversize request on a variable-price alias must be refused before dispatch")
	}
	if got != nil {
		t.Error("a refused request must not hand back a body to dispatch")
	}
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	// Provider-blind: the refusal must not name the provider or the upstream.
	for _, forbidden := range []string{"openrouter", "OpenRouter", "auto-beta", "anthropic"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Errorf("refusal leaked %q: %s", forbidden, w.Body.String())
		}
	}
}

func TestEnforceVariablePriceBounds_LeavesFixedPriceAliasesAlone(t *testing.T) {
	route := SelectRouteResult{Pricing: FixedPricing(10_500, 42_000), PriceUnit: PriceUnitTokens}
	body := []byte(`{"messages":"` + strings.Repeat("x", VariablePriceMaxRequestBytes) + `"}`)

	w := httptest.NewRecorder()
	got, ok := EnforceVariablePriceBounds(w, route, EndpointChatCompletions, "hive-fast", body)
	if !ok {
		t.Fatal("a fixed-price alias must not be subject to the variable-price size cap")
	}
	if string(got) != string(body) {
		t.Error("a fixed-price alias's body must pass through byte for byte")
	}
}

func TestEnforceVariablePriceBounds_PinsTheCompletionCeiling(t *testing.T) {
	route := SelectRouteResult{Pricing: UpstreamActualPricing(200_000), PriceUnit: PriceUnitTokens}

	tests := []struct {
		name     string
		endpoint string
		body     string
		field    string
		want     int64
	}{
		{
			name:     "absent: the client did not ask for a ceiling, so one is imposed",
			endpoint: EndpointChatCompletions,
			body:     `{"model":"openrouter-auto"}`,
			field:    "max_tokens",
			want:     VariablePriceMaxCompletionTokens,
		},
		{
			name:     "above the cap: a client cannot raise it",
			endpoint: EndpointChatCompletions,
			body:     `{"model":"openrouter-auto","max_tokens":900000}`,
			field:    "max_tokens",
			want:     VariablePriceMaxCompletionTokens,
		},
		{
			name:     "below the cap: the client's own smaller limit is respected",
			endpoint: EndpointChatCompletions,
			body:     `{"model":"openrouter-auto","max_tokens":64}`,
			field:    "max_tokens",
			want:     64,
		},
		{
			name:     "unreadable value is replaced rather than trusted",
			endpoint: EndpointChatCompletions,
			body:     `{"model":"openrouter-auto","max_tokens":"lots"}`,
			field:    "max_tokens",
			want:     VariablePriceMaxCompletionTokens,
		},
		{
			name:     "the responses endpoint uses its own field name",
			endpoint: EndpointResponses,
			body:     `{"model":"openrouter-auto"}`,
			field:    "max_output_tokens",
			want:     VariablePriceMaxCompletionTokens,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			got, ok := EnforceVariablePriceBounds(w, route, tc.endpoint, "openrouter-auto", []byte(tc.body))
			if !ok {
				t.Fatalf("unexpected refusal: %s", w.Body.String())
			}
			var decoded map[string]any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("bounded body is not valid JSON: %v", err)
			}
			value, present := decoded[tc.field]
			if !present {
				t.Fatalf("%s missing from the bounded body: %s", tc.field, got)
			}
			number, _ := value.(float64)
			if int64(number) != tc.want {
				t.Errorf("%s = %v, want %d", tc.field, value, tc.want)
			}
			// Everything else the caller sent must survive untouched.
			if decoded["model"] != "openrouter-auto" {
				t.Errorf("the caller's own fields must survive; got %v", decoded)
			}
		})
	}
}

func TestEnforceVariablePriceBounds_RefusesAnUnparseableBody(t *testing.T) {
	route := SelectRouteResult{Pricing: UpstreamActualPricing(200_000), PriceUnit: PriceUnitTokens}
	w := httptest.NewRecorder()
	if _, ok := EnforceVariablePriceBounds(w, route, EndpointChatCompletions, "openrouter-auto", []byte(`not json`)); ok {
		t.Fatal("a body that cannot be bounded must be refused, not dispatched unbounded")
	}
}

// The invariant this whole mechanism exists for, checked as arithmetic across
// the three files that have to agree: the LiteLLM price ceiling, the request
// bounds in this package, and the hold in the migration.
//
// This is a real cross-file guard, not a restatement. Raise the ceiling in the
// YAML, or either cap here, without re-deriving the hold, and it fails.
func TestTheHoldProvablyCoversTheWorstBoundedRequest(t *testing.T) {
	promptCeiling, completionCeiling := readMaxPriceCeiling(t)
	hold := readReservationEstimate(t)

	// Worst case, in USD, as an exact rational:
	//   prompt bytes are a rigorous upper bound on prompt tokens
	//   completion is capped outright
	worst := new(big.Rat).Add(
		new(big.Rat).Mul(
			new(big.Rat).SetInt64(VariablePriceMaxRequestBytes),
			new(big.Rat).Quo(promptCeiling, new(big.Rat).SetInt64(1_000_000)),
		),
		new(big.Rat).Mul(
			new(big.Rat).SetInt64(VariablePriceMaxCompletionTokens),
			new(big.Rat).Quo(completionCeiling, new(big.Rat).SetInt64(1_000_000)),
		),
	)

	worstCredits, err := CreditsForUpstreamCost(worst)
	if err != nil {
		t.Fatalf("could not price the worst case: %v", err)
	}

	if worstCredits > hold {
		t.Fatalf("the hold does NOT cover the worst bounded request: worst case is %d credits but the catalog holds %d. "+
			"Either lower provider.max_price in deploy/litellm/config.yaml, lower the request bounds in this package, "+
			"or raise reservation_estimate_credits in the migration. These three numbers are one decision.",
			worstCredits, hold)
	}
	t.Logf("worst bounded request = %d credits, hold = %d credits, headroom = %d",
		worstCredits, hold, hold-worstCredits)
}

// readMaxPriceCeiling pulls provider.max_price for the variable-price route out
// of the real LiteLLM config, in USD per million tokens.
func readMaxPriceCeiling(t *testing.T) (prompt, completion *big.Rat) {
	t.Helper()
	const path = "../../../../deploy/litellm/config.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s, which this guard depends on: %v", path, err)
	}

	idx := strings.Index(string(raw), "model_name: route-openrouter-auto-beta")
	if idx < 0 {
		t.Fatal("route-openrouter-auto-beta is not in deploy/litellm/config.yaml; if it was renamed, move this guard with it")
	}
	block := string(raw)[idx:]

	prompt = matchRat(t, block, `max_price:\s*\n\s*prompt:\s*([0-9.]+)`, "prompt")
	completion = matchRat(t, block, `completion:\s*([0-9.]+)`, "completion")
	return prompt, completion
}

func matchRat(t *testing.T, block, pattern, label string) *big.Rat {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("no %s ceiling found on the variable-price route; the bound this guard checks is not configured", label)
	}
	value, ok := new(big.Rat).SetString(m[1])
	if !ok {
		t.Fatalf("%s ceiling %q is not a number", label, m[1])
	}
	return value
}

// readReservationEstimate pulls the hold out of the migration that seeds the
// alias, then applies any later rescale migrations on top, so the guard reads
// the value as shipped rather than a copy of it.
func readReservationEstimate(t *testing.T) int64 {
	t.Helper()
	const path = "../../../../supabase/migrations/20260822_30_openrouter_auto_variable_pricing.sql"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s, which this guard depends on: %v", path, err)
	}
	// The INSERT ends with the hold as its last value, after 'upstream_actual'.
	m := regexp.MustCompile(`'upstream_actual',\s*\n\s*([0-9_]+)`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("could not find reservation_estimate_credits in the migration; if the INSERT changed shape, move this guard with it")
	}
	held, err := strconv.ParseInt(strings.ReplaceAll(string(m[1]), "_", ""), 10, 64)
	if err != nil {
		t.Fatalf("hold %q is not a number: %v", m[1], err)
	}

	// The credit unit rescale (2026-08-23) multiplied every stored credit
	// figure by a fixed factor, including this hold. Parse the factor out of
	// that migration rather than hardcoding it, so the guard tracks whatever
	// the database actually holds.
	const rescalePath = "../../../../supabase/migrations/20260823_40_credit_unit_rescale_billion.sql"
	rescaleRaw, err := os.ReadFile(rescalePath)
	if err != nil {
		t.Fatalf("cannot read %s, which this guard depends on: %v", rescalePath, err)
	}
	f := regexp.MustCompile(`factor\s+CONSTANT\s+bigint\s*:=\s*([0-9]+)`).FindSubmatch(rescaleRaw)
	mul := regexp.MustCompile(`reservation_estimate_credits\s*=\s*reservation_estimate_credits \* factor`).Match(rescaleRaw)
	if f == nil || !mul {
		t.Fatal("the rescale migration no longer multiplies reservation_estimate_credits by its declared factor; if it rescales the hold differently, update this guard to match")
	}
	factor, err := strconv.ParseInt(string(f[1]), 10, 64)
	if err != nil || factor <= 0 {
		t.Fatalf("rescale factor %q is not a positive number", f[1])
	}
	held *= factor

	if held <= 0 {
		t.Fatalf("hold must be positive, got %d", held)
	}
	return held
}

var _ = fmt.Sprintf
