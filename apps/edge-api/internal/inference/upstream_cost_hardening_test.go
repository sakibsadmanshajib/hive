package inference

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// Guards for the defects the adversarial review found in the first two commits.
// Each one is a case where the earlier code produced a plausible number instead
// of a refusal, which on a money path is the worst failure shape there is.

// --- The hold has to come off the key the control plane actually sends ------

// ReservationResult.EstimatedCredits reads `estimated_credits`, which the
// control plane does NOT publish; it sends `reserved_credits`. Nothing read
// that field before variable pricing did, so the mismatch was invisible until a
// charge depended on it, at which point a failed cost lookup settled at 1
// credit rather than at the hold.
func TestReservationHoldReadsTheKeyTheControlPlaneActuallySends(t *testing.T) {
	// Byte for byte the shape apps/control-plane/internal/accounting/types.go
	// marshals, trimmed to the fields that matter here.
	body := []byte(`{"id":"res-1","status":"active","reserved_credits":200000,` +
		`"consumed_credits":0,"released_credits":0}`)

	var got ReservationResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.EstimatedCredits != 0 {
		t.Fatalf("fixture drift: the control plane does not send estimated_credits, got %d", got.EstimatedCredits)
	}
	if got.Held() != 200_000 {
		t.Fatalf("Held() = %d, want 200000. A zero here makes every fail-closed settlement charge 1 credit "+
			"instead of the hold, which is the free-serve bug wearing a different hat.", got.Held())
	}
}

func TestReservationHoldFallsBackToTheLegacyKey(t *testing.T) {
	var got ReservationResult
	if err := json.Unmarshal([]byte(`{"id":"res-1","estimated_credits":1234}`), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Held() != 1234 {
		t.Fatalf("Held() = %d, want the legacy key honoured when it is the only one present", got.Held())
	}
}

// --- An oversized cost must refuse, not wrap into a tiny charge ------------

func TestCreditsForUpstreamCostRefusesImplausibleMagnitudes(t *testing.T) {
	// 1e20 USD. The old code multiplied, called big.Int.Int64() on a value that
	// does not fit, got the low 64 bits with the sign reinterpreted, and the
	// `credits < 1` floor turned the resulting negative into a charge of ONE
	// credit, returned as confirmed.
	huge, ok := new(big.Rat).SetString("1e20")
	if !ok {
		t.Fatal("bad fixture")
	}

	credits, err := CreditsForUpstreamCost(huge)
	if !errors.Is(err, ErrUpstreamCostImplausible) {
		t.Fatalf("expected an implausible-cost refusal, got credits=%d err=%v", credits, err)
	}
	if credits == 1 {
		t.Fatal("1e20 USD settled at ONE credit: the int64 wrap is back")
	}

	// Just over the per-request ceiling must refuse too, well before any
	// overflow, so the guard does not depend on the wrap to catch things.
	overCeiling, _ := new(big.Rat).SetString("100")
	if _, err := CreditsForUpstreamCost(overCeiling); !errors.Is(err, ErrUpstreamCostImplausible) {
		t.Fatalf("100 USD is %d credits, above the per-request ceiling, and must refuse; got %v",
			100*CreditsPerUSD, err)
	}

	// And the ceiling must not be so tight that ordinary traffic trips it.
	fine, _ := new(big.Rat).SetString("0.0123456")
	if _, err := CreditsForUpstreamCost(fine); err != nil {
		t.Fatalf("an ordinary cost must still settle, got %v", err)
	}
}

// --- A huge numeric literal must be refused before big.Rat parses it -------

func TestParseUpstreamCostRefusesAnOversizedLiteralWithoutParsingIt(t *testing.T) {
	// json.Number only guarantees JSON number syntax, and JSON puts no cap on
	// digits. big.Rat would faithfully build the exact rational, spending the
	// CPU inside the request at the caller's choosing.
	literal := "0." + strings.Repeat("9", 100_000)
	body := []byte(`{"id":"gen-1","usage":{"prompt_tokens":10,"completion_tokens":5,"cost":` + literal + `}}`)

	_, err := ParseUpstreamCost(body)
	if !errors.Is(err, ErrUpstreamCostUnparseable) {
		t.Fatalf("expected an oversized literal to be refused, got %v", err)
	}

	// A normal literal of realistic precision must still be accepted.
	okBody := []byte(`{"id":"gen-1","usage":{"prompt_tokens":10,"completion_tokens":5,"cost":0.000000123456789}}`)
	if _, err := ParseUpstreamCost(okBody); err != nil {
		t.Fatalf("a realistic cost literal must still parse, got %v", err)
	}
}

// --- Nothing the upstream adds may be forwarded to the customer ------------

func TestSanitizeVariablePriceFrameStripsEverythingConfidential(t *testing.T) {
	frame := []byte(`{"id":"gen-1","object":"chat.completion.chunk","provider":"Anthropic",` +
		`"system_fingerprint":"fp_deadbeef",` +
		`"model":"anthropic/claude-sonnet-4.5",` +
		`"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}],` +
		`"usage":{"prompt_tokens":1000,"completion_tokens":500,"cost":0.0123456,` +
		`"is_byok":false,"cost_details":{"upstream_inference_cost":0.0123456}}}`)

	mintedID := "chatcmpl-test-stable-id"
	out, ok := SanitizeVariablePriceFrame(frame, "openrouter-auto", mintedID)
	if !ok {
		t.Fatal("a well-formed frame must sanitize, not be dropped")
	}
	s := string(out)

	// id and system_fingerprint are upstream-identity leaks: OpenRouter's own
	// "gen-*" id shape and any provider's system_fingerprint both name the
	// provider by construction, exactly like the sonnet/anthropic strings
	// below.
	for _, forbidden := range []string{
		"Anthropic", "claude-sonnet-4.5", "0.0123456", "cost_details", "is_byok", `"provider"`, `"cost"`,
		"gen-1", "fp_deadbeef", `"system_fingerprint"`,
	} {
		if strings.Contains(s, forbidden) {
			t.Errorf("sanitized frame still leaks %q: %s", forbidden, s)
		}
	}

	// Everything the client legitimately needs must survive, including the
	// token counts and the delta content. The id is rewritten, not dropped:
	// the caller's stream-stable mintedID must be present so every chunk of
	// one stream keeps carrying the same client-visible id.
	for _, required := range []string{"openrouter-auto", "prompt_tokens", "completion_tokens", `"hi"`, "chat.completion.chunk", mintedID} {
		if !strings.Contains(s, required) {
			t.Errorf("sanitized frame dropped %q, which the client needs: %s", required, s)
		}
	}
}

// TestSanitizeVariablePriceFrameRewritesIDToMintedID guards the id-stability
// contract this fallback path shares with the typed relay in
// executeStreaming: a client must see the SAME id on every chunk of one
// stream, even the rare chunk that falls back to this map-based sanitizer
// because typed decoding failed on it. Deleting the id outright (rather than
// rewriting it) would break that contract the moment this fallback fires
// mid-stream.
func TestSanitizeVariablePriceFrameRewritesIDToMintedID(t *testing.T) {
	frame := []byte(`{"id":"gen-should-never-reach-client","object":"chat.completion.chunk","choices":[]}`)
	mintedID := "chatcmpl-fixed-for-this-stream"

	out, ok := SanitizeVariablePriceFrame(frame, "openrouter-auto", mintedID)
	if !ok {
		t.Fatal("a well-formed frame must sanitize, not be dropped")
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	var gotID string
	if err := json.Unmarshal(got["id"], &gotID); err != nil {
		t.Fatal(err)
	}
	if gotID != mintedID {
		t.Errorf("id: want mintedID %q, got %q", mintedID, gotID)
	}
}

func TestSanitizeVariablePriceFrameDropsWhatItCannotParse(t *testing.T) {
	// An unparseable frame is exactly the one whose contents are unknown, so
	// the caller must drop it rather than forward it.
	if _, ok := SanitizeVariablePriceFrame([]byte(`{"usage": nope}`), "openrouter-auto", "chatcmpl-irrelevant"); ok {
		t.Fatal("an unparseable frame must not be reported as safe to forward")
	}
}
