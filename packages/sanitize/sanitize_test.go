package sanitize

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMintID_PrefixShapeAndUniqueness(t *testing.T) {
	a := MintID("chatcmpl")
	b := MintID("chatcmpl")
	if !strings.HasPrefix(a, "chatcmpl-") || !strings.HasPrefix(b, "chatcmpl-") {
		t.Fatalf("want chatcmpl- prefix, got %q / %q", a, b)
	}
	if a == b {
		t.Fatalf("expected two distinct minted ids, got the same value twice: %q", a)
	}
}

func TestVariablePriceFrame_StripsUpstreamIdentityAndCost(t *testing.T) {
	raw := `{"id":"gen-1787946282-BraVtgcskggFgHSaafrV","model":"route-deepseek-v4-pro","system_fingerprint":"fp_deadbeef","provider":"DigitalOcean","choices":[{"index":0}],"usage":{"prompt_tokens":9,"completion_tokens":3,"cost":2.376e-05,"is_byok":false,"cost_details":{"upstream_inference_cost":2.376e-05}}}`

	out, ok := VariablePriceFrame([]byte(raw), "customer-alias-1", "chatcmpl-minted-1")
	if !ok {
		t.Fatalf("VariablePriceFrame reported not ok on well-formed input")
	}
	body := string(out)

	for _, leak := range []string{
		"gen-1787946282-BraVtgcskggFgHSaafrV",
		"DigitalOcean",
		"\"provider\"",
		"system_fingerprint",
		"\"cost\"",
		"cost_details",
		"is_byok",
		"route-deepseek-v4-pro",
	} {
		if strings.Contains(body, leak) {
			t.Fatalf("sanitized frame leaked %q:\n%s", leak, body)
		}
	}

	var decoded struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("sanitized frame is not valid JSON: %v\n%s", err, body)
	}
	if decoded.ID != "chatcmpl-minted-1" {
		t.Fatalf("id=%q want minted id", decoded.ID)
	}
	if decoded.Model != "customer-alias-1" {
		t.Fatalf("model=%q want customer alias", decoded.Model)
	}
	// Legitimate usage fields survive the strip.
	if decoded.Usage.PromptTokens != 9 || decoded.Usage.CompletionTokens != 3 {
		t.Fatalf("usage token counts corrupted: %+v", decoded.Usage)
	}
}

func TestVariablePriceFrame_NoOpOnFrameWithoutCostFields(t *testing.T) {
	raw := `{"id":"gen-x","model":"route-groq-fast","choices":[{"index":0}]}`
	out, ok := VariablePriceFrame([]byte(raw), "alias", "minted")
	if !ok {
		t.Fatalf("expected ok on frame with no usage/cost block")
	}
	if strings.Contains(string(out), "gen-x") || strings.Contains(string(out), "route-groq-fast") {
		t.Fatalf("id/model not rewritten: %s", out)
	}
}

func TestVariablePriceFrame_UnparseablePayloadReturnsNotOK(t *testing.T) {
	if _, ok := VariablePriceFrame([]byte("not json"), "alias", "minted"); ok {
		t.Fatalf("expected ok=false on malformed payload")
	}
}
