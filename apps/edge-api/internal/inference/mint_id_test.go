package inference

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- normalizeChatCompletion: id minting + system_fingerprint strip ---

// TestNormalizeChatCompletion_MintsGatewayID_StripsSystemFingerprint
// reproduces the live parity finding (2026-08-26, /tmp/reports/parity-inhive.md):
// OpenRouter-routed responses forwarded their own "gen-*" id verbatim, and
// Groq-family responses forwarded "chatcmpl-*" plus a system_fingerprint
// field, both letting upstream provider identity reach the client in
// violation of CLAUDE.md's provider-blind invariant.
func TestNormalizeChatCompletion_MintsGatewayID_StripsSystemFingerprint(t *testing.T) {
	cases := []struct {
		name       string
		upstreamID string
		body       string
	}{
		{
			name:       "openrouter gen- id format",
			upstreamID: "gen-1787730057-srEROy2TAO1R0R8RdrwH",
			body: `{"id":"gen-1787730057-srEROy2TAO1R0R8RdrwH","object":"chat.completion","created":0,` +
				`"model":"route-deepseek","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` +
				`"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}}`,
		},
		{
			name:       "groq chatcmpl- id format plus system_fingerprint",
			upstreamID: "chatcmpl-8f3a9c2e1b4d",
			body: `{"id":"chatcmpl-8f3a9c2e1b4d","object":"chat.completion","created":0,` +
				`"model":"route-groq","system_fingerprint":"fp_44709d6fcb",` +
				`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` +
				`"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalized, _, err := normalizeChatCompletion([]byte(tc.body), "deepseek-v4-flash")
			if err != nil {
				t.Fatal(err)
			}

			var resp ChatCompletionResponse
			if err := json.Unmarshal(normalized, &resp); err != nil {
				t.Fatal(err)
			}
			if resp.ID == tc.upstreamID {
				t.Errorf("id: must never echo the upstream id verbatim, got %q", resp.ID)
			}
			if resp.ID[:9] != "chatcmpl-" {
				t.Errorf("id: want chatcmpl- prefix, got %q", resp.ID)
			}
			if resp.SystemFingerprint != nil {
				t.Errorf("system_fingerprint: want stripped, got %q", *resp.SystemFingerprint)
			}

			// Wire-level check: the raw bytes sent to the client must not
			// contain the upstream id or a system_fingerprint key at all,
			// not just the typed struct.
			s := string(normalized)
			for _, forbidden := range []string{tc.upstreamID, "system_fingerprint"} {
				if strings.Contains(s, forbidden) {
					t.Errorf("normalized response leaks %q on the wire: %s", forbidden, s)
				}
			}
		})
	}
}

// --- normalizeCompletion (legacy /v1/completions): id minting ---

func TestNormalizeCompletion_MintsGatewayID(t *testing.T) {
	upstreamID := "gen-legacy-abc123"
	body := `{"id":"gen-legacy-abc123","object":"text_completion","created":0,"model":"route-deepseek",` +
		`"choices":[{"text":"hi","index":0,"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}}`

	normalized, _, err := normalizeCompletion([]byte(body), "deepseek-v4-flash")
	if err != nil {
		t.Fatal(err)
	}

	var resp CompletionResponse
	if err := json.Unmarshal(normalized, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == upstreamID {
		t.Errorf("id: must never echo the upstream id verbatim, got %q", resp.ID)
	}
	if resp.ID[:5] != "cmpl-" {
		t.Errorf("id: want cmpl- prefix, got %q", resp.ID)
	}
}

// --- mintCompletionID / idPrefixForEndpoint ---

func TestMintCompletionID_UniquePerCall(t *testing.T) {
	a := mintCompletionID("chatcmpl")
	b := mintCompletionID("chatcmpl")
	if a == b {
		t.Fatal("two mints in a row produced the same id")
	}
}

func TestIdPrefixForEndpoint(t *testing.T) {
	if got := idPrefixForEndpoint(EndpointCompletions); got != "cmpl" {
		t.Errorf("EndpointCompletions: want cmpl, got %q", got)
	}
	if got := idPrefixForEndpoint(EndpointChatCompletions); got != "chatcmpl" {
		t.Errorf("EndpointChatCompletions: want chatcmpl, got %q", got)
	}
}

// --- DeepSeek post-finish spurious chunk suppression ---

func TestChunkFinished(t *testing.T) {
	stop := "stop"
	empty := ""
	cases := []struct {
		name  string
		chunk ChatCompletionChunk
		want  bool
	}{
		{"no choices", ChatCompletionChunk{}, false},
		{"finish reason nil", ChatCompletionChunk{Choices: []ChunkChoice{{FinishReason: nil}}}, false},
		{"finish reason empty string", ChatCompletionChunk{Choices: []ChunkChoice{{FinishReason: &empty}}}, false},
		{"finish reason stop", ChatCompletionChunk{Choices: []ChunkChoice{{FinishReason: &stop}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChunkFinished(tc.chunk); got != tc.want {
				t.Errorf("ChunkFinished() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestShouldSuppressPostFinishChunk_DeepSeekSpuriousFrame reproduces the
// exact sequence captured live in /tmp/parity/st-deepseek-v4-flash.sse: a
// real finish_reason=stop chunk, followed by one extra chunk carrying
// delta.role="assistant" and empty content with finish_reason=null, before
// [DONE]. The second chunk must be suppressed.
func TestShouldSuppressPostFinishChunk_DeepSeekSpuriousFrame(t *testing.T) {
	spurious := ChatCompletionChunk{
		Choices: []ChunkChoice{{Delta: ChunkDelta{Content: strPtr("")}}},
	}
	if !ShouldSuppressPostFinishChunk(true, spurious) {
		t.Error("a no-usage chunk arriving after finish must be suppressed")
	}
	if ShouldSuppressPostFinishChunk(false, spurious) {
		t.Error("before any finish has been seen, nothing is suppressed")
	}
}

// TestShouldSuppressPostFinishChunk_UsageOnlyTerminalFrameStillForwards
// guards the one legitimate exception: stream_options.include_usage
// delivers cost data in its own frame after finish_reason by design, and
// that must never be dropped.
func TestShouldSuppressPostFinishChunk_UsageOnlyTerminalFrameStillForwards(t *testing.T) {
	usageOnly := ChatCompletionChunk{Usage: &UsageResponse{PromptTokens: 9, CompletionTokens: 2, TotalTokens: 11}}
	if ShouldSuppressPostFinishChunk(true, usageOnly) {
		t.Error("a usage-only, zero-choices chunk must never be suppressed, even after finish")
	}
}

// TestShouldSuppressPostFinishChunk_UsageWithContentStillSuppressed guards
// against narrowing the exception too far: usage presence alone is not
// sufficient, only a genuine usage-only (zero choices) terminal shape is. A
// chunk that carries both usage AND actual choice content after finish is
// still spurious and must be suppressed.
func TestShouldSuppressPostFinishChunk_UsageWithContentStillSuppressed(t *testing.T) {
	usageWithContent := ChatCompletionChunk{
		Usage:   &UsageResponse{PromptTokens: 9, CompletionTokens: 2, TotalTokens: 11},
		Choices: []ChunkChoice{{Delta: ChunkDelta{Content: strPtr("hi")}}},
	}
	if !ShouldSuppressPostFinishChunk(true, usageWithContent) {
		t.Error("a chunk with usage AND non-empty choices after finish must still be suppressed")
	}
}
