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

// TestVariablePriceFrame_NullPayloadReturnsNotOK -- issue #1253 review
// (CodeRabbit): json.Unmarshal(nil, &frame) for a JSON "null" body sets
// frame to a nil map with NO error, so the unparseable-payload guard alone
// does not catch it. Left unchecked, every "present" guard is then false
// and json.Marshal(nil map) legally re-encodes to "null" -- an
// empty-but-valid frame the caller would store as a completed success.
func TestVariablePriceFrame_NullPayloadReturnsNotOK(t *testing.T) {
	if _, ok := VariablePriceFrame([]byte("null"), "alias", "minted"); ok {
		t.Fatalf("expected ok=false on a JSON null payload")
	}
}

// TestVariablePriceFrame_TopLevelErrorReturnsNotOK -- issue #1253 review:
// a 2xx status body carrying a top-level "error" object is an upstream
// failure delivered inside a success status (observed live from
// OpenRouter, whose error.metadata.provider_name/raw fields carry upstream
// identity and the upstream's own error text). Before this fix, nothing in
// this function inspected "error" at all, so that shape sanitized cleanly
// and would have been stored as a completed line.
func TestVariablePriceFrame_TopLevelErrorReturnsNotOK(t *testing.T) {
	raw := `{"id":"gen-x","model":"route-x","error":{"message":"rate limited upstream","metadata":{"provider_name":"OpenRouter","raw":"upstream said no"}}}`
	if _, ok := VariablePriceFrame([]byte(raw), "alias", "minted"); ok {
		t.Fatalf("expected ok=false on a frame carrying a top-level error object")
	}
}

// TestVariablePriceFrame_DropsUnknownTopLevelKey is the production-side
// half of issue #1253 review finding H2: the sanitizer itself now
// allowlists top-level keys, not only the test fixture. A key this
// package has never modeled (an "upstream_provider" field, a future
// vendor extension) must be dropped by VariablePriceFrame directly, not
// merely detected after the fact by a separate keyset test.
func TestVariablePriceFrame_DropsUnknownTopLevelKey(t *testing.T) {
	raw := `{"id":"gen-x","model":"route-x","choices":[{"index":0}],"upstream_provider":"DigitalOcean","x_never_seen_before":{"anything":"at all"}}`
	out, ok := VariablePriceFrame([]byte(raw), "alias", "minted")
	if !ok {
		t.Fatalf("VariablePriceFrame reported not ok on well-formed input")
	}
	for _, unknown := range []string{"upstream_provider", "x_never_seen_before", "DigitalOcean"} {
		if strings.Contains(string(out), unknown) {
			t.Fatalf("sanitized frame kept an unallowlisted key/value %q:\n%s", unknown, out)
		}
	}
}

func TestVariablePriceFrame_StripsProviderSpecificFieldsFromChoices(t *testing.T) {
	// issue #1280: OpenRouter's own provider_specific_fields wrapper,
	// observed live nested at both the choice level and choice.message
	// level. Its presence, independent of its contents, is a routing-layer
	// identity signal, so it is stripped wholesale rather than allowlisted
	// by content.
	raw := `{"id":"gen-x","model":"route-x","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi","provider_specific_fields":{"reasoning":null,"refusal":null}},"provider_specific_fields":{"native_finish_reason":"stop"}}]}`
	out, ok := VariablePriceFrame([]byte(raw), "alias", "minted")
	if !ok {
		t.Fatalf("VariablePriceFrame reported not ok on well-formed input")
	}
	body := string(out)
	for _, leak := range []string{"provider_specific_fields", "native_finish_reason", "reasoning", "refusal"} {
		if strings.Contains(body, leak) {
			t.Fatalf("sanitized frame leaked %q:\n%s", leak, body)
		}
	}
	// The rest of the choice/message content must survive untouched.
	for _, keep := range []string{"finish_reason", "stop", "role", "assistant", "content", "\"hi\""} {
		if !strings.Contains(body, keep) {
			t.Fatalf("sanitized frame lost legitimate field %q:\n%s", keep, body)
		}
	}
}

func TestVariablePriceFrame_ChoicesNotArrayReturnsNotOK(t *testing.T) {
	// A frame whose "choices" key is present but not an array of objects is
	// exactly the "contents are unknown" case the package's own doc comment
	// says must fail closed rather than being forwarded.
	raw := `{"id":"gen-x","model":"route-x","choices":"not an array"}`
	if _, ok := VariablePriceFrame([]byte(raw), "alias", "minted"); ok {
		t.Fatalf("expected ok=false when choices is not an array")
	}
}

// collectKeys recursively walks a decoded JSON value (from
// json.Unmarshal(..., &any)) and records every object key found at every
// nesting depth, regardless of path. Used by the keyset-allowlist test
// below: it does not care WHERE a key appears, only THAT it appears.
func collectKeys(v any, out map[string]bool) {
	switch val := v.(type) {
	case map[string]any:
		for k, sub := range val {
			out[k] = true
			collectKeys(sub, out)
		}
	case []any:
		for _, sub := range val {
			collectKeys(sub, out)
		}
	}
}

// TestVariablePriceFrame_KeysetAllowlist_CatchesUnforeseenFields is issue
// #1253's review finding H2: every other test in this file asserts on
// specific known-bad substrings (strings.Contains for a handful of named
// leaks), which cannot go red for a field nobody thought to list, since
// VariablePriceFrame itself deletes by explicit key name and never
// recurses beyond usage/choices. A future usage.cost_breakdown sibling, a
// new per-choice metadata block, or a top-level upstream_provider field
// would pass straight through both the sanitizer and every
// strings.Contains test, silently.
//
// This test instead walks EVERY key at EVERY nesting depth in the
// sanitized output and demands each one already be on an explicit
// allowlist, using the full, untrimmed real capture (2026-08-28,
// route-deepseek-v4-pro) as the fixture. Any key not on the allowlist
// fails loudly, forcing a human decision (extend the sanitizer, or extend
// the allowlist deliberately) instead of a silent pass-through. It cannot
// detect a field that has never appeared in any captured fixture ever,
// only that this file's own fixture stays honest about what actually
// survives sanitization.
func TestVariablePriceFrame_KeysetAllowlist_CatchesUnforeseenFields(t *testing.T) {
	const raw = `{"id":"gen-1787946282-BraVtgcskggFgHSaafrV","created":1787946282,"model":"route-deepseek-v4-pro","object":"chat.completion","choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hi!","role":"assistant","provider_specific_fields":{"reasoning":null,"refusal":null}},"provider_specific_fields":{"native_finish_reason":"stop"}}],"usage":{"completion_tokens":3,"prompt_tokens":9,"total_tokens":12,"completion_tokens_details":{"audio_tokens":0,"reasoning_tokens":0,"image_tokens":0},"prompt_tokens_details":{"audio_tokens":0,"cached_tokens":0,"video_tokens":0,"cache_write_tokens":0,"cache_creation_tokens":0},"cost":2.376e-05,"is_byok":false,"cost_details":{"upstream_inference_cost":2.376e-05,"upstream_inference_prompt_cost":1.188e-05,"upstream_inference_completions_cost":1.188e-05}},"provider":"DigitalOcean"}`

	out, ok := VariablePriceFrame([]byte(raw), "customer-alias-1", "chatcmpl-minted-1")
	if !ok {
		t.Fatalf("VariablePriceFrame reported not ok on well-formed input")
	}

	var decoded any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("sanitized frame is not valid JSON: %v", err)
	}

	// Every key legitimately expected to survive a chat-completion frame's
	// sanitization, at any nesting depth. Adding a key here is a
	// deliberate, reviewed decision, not a side effect of adding an
	// upstream field.
	allowlist := map[string]bool{
		"id": true, "object": true, "created": true, "model": true,
		"choices": true, "index": true, "finish_reason": true, "logprobs": true,
		"message": true, "role": true, "content": true,
		"tool_calls": true, "function_call": true,
		"usage": true, "prompt_tokens": true, "completion_tokens": true, "total_tokens": true,
		"completion_tokens_details": true, "prompt_tokens_details": true,
		"audio_tokens": true, "reasoning_tokens": true, "image_tokens": true,
		"cached_tokens": true, "video_tokens": true, "cache_write_tokens": true, "cache_creation_tokens": true,
	}

	seenKeys := map[string]bool{}
	collectKeys(decoded, seenKeys)

	for k := range seenKeys {
		if !allowlist[k] {
			t.Errorf("sanitized output contains key %q, not on the allowlist: either a leak the sanitizer missed, or the allowlist needs a deliberate, reviewed addition", k)
		}
	}

	// The known leak keys, and their contents observed live, must be gone
	// entirely, not merely emptied.
	for _, leaked := range []string{
		"provider", "system_fingerprint", "cost", "cost_details", "is_byok",
		"provider_specific_fields", "native_finish_reason", "reasoning", "refusal",
		"upstream_inference_cost", "upstream_inference_prompt_cost", "upstream_inference_completions_cost",
	} {
		if seenKeys[leaked] {
			t.Errorf("sanitized output still contains leaked key %q", leaked)
		}
	}
}
