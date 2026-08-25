package inference

import (
	"bytes"
	"log"
	"os"
	"testing"
)

// TestNormalizeCacheUsageInclusiveShapeSubtracts is the OpenAI/OpenRouter
// direction: prompt_tokens already counts the cached subset, so fresh must
// be obtained by SUBTRACTING both cache components, never by adding them.
// OpenRouter normalizes even an Anthropic-backed model to this shape before
// Hive ever sees the response, so this branch is the one real traffic
// actually takes today.
func TestNormalizeCacheUsageInclusiveShapeSubtracts(t *testing.T) {
	usage := &UsageResponse{
		PromptTokens: 205_000,
		PromptTokensDetails: &PromptTokensDetails{
			CachedTokens:     200_000,
			CacheWriteTokens: 3_000,
		},
	}
	got := NormalizeCacheUsage(usage, "hive-fast", "groq")

	if got.FreshInputTokens != 2_000 {
		t.Errorf("FreshInputTokens = %d, want 2000 (205000 - 200000 - 3000)", got.FreshInputTokens)
	}
	if got.CacheReadTokens != 200_000 {
		t.Errorf("CacheReadTokens = %d, want 200000", got.CacheReadTokens)
	}
	if got.CacheWriteTokens != 3_000 {
		t.Errorf("CacheWriteTokens = %d, want 3000", got.CacheWriteTokens)
	}
	// Partition invariant: the three components reconstruct the wire total
	// exactly, for every shape.
	if sum := got.FreshInputTokens + got.CacheReadTokens + got.CacheWriteTokens; sum != usage.PromptTokens {
		t.Errorf("fresh + cache_read + cache_write = %d, want exactly prompt_tokens = %d", sum, usage.PromptTokens)
	}
}

// TestNormalizeCacheUsageExclusiveShapeAdds is the Anthropic-native
// direction: prompt_tokens already excludes both cache components, so fresh
// is prompt_tokens UNCHANGED, and total input is obtained by ADDING, never
// subtracting. Reachable only through a future direct (non-LiteLLM)
// dispatch; detected by the presence of Anthropic's own field names, never
// by model name.
func TestNormalizeCacheUsageExclusiveShapeAdds(t *testing.T) {
	cacheRead := int64(200_000)
	cacheWrite := int64(3_000)
	usage := &UsageResponse{
		PromptTokens:             5_000,
		CacheReadInputTokens:     &cacheRead,
		CacheCreationInputTokens: &cacheWrite,
	}
	got := NormalizeCacheUsage(usage, "claude-direct", "anthropic")

	if got.FreshInputTokens != 5_000 {
		t.Errorf("FreshInputTokens = %d, want 5000 (unchanged)", got.FreshInputTokens)
	}
	if got.CacheReadTokens != 200_000 {
		t.Errorf("CacheReadTokens = %d, want 200000", got.CacheReadTokens)
	}
	if got.CacheWriteTokens != 3_000 {
		t.Errorf("CacheWriteTokens = %d, want 3000", got.CacheWriteTokens)
	}
	totalInput := got.FreshInputTokens + got.CacheReadTokens + got.CacheWriteTokens
	if totalInput != 208_000 {
		t.Errorf("total input = %d, want 208000 (5000 + 200000 + 3000, added not subtracted)", totalInput)
	}
}

// TestNormalizeCacheUsageShapeSelectedByWireFieldsNotModelName is the
// specific trap the cache-pricing contract calls out: an OpenRouter-proxied
// Claude response is still the INCLUSIVE shape, because OpenRouter itself
// normalizes it before Hive ever sees the response. Branching on the model
// name ("is this alias a claude/anthropic model") would take the wrong
// branch here and nearly double the input charge.
func TestNormalizeCacheUsageShapeSelectedByWireFieldsNotModelName(t *testing.T) {
	usage := &UsageResponse{
		PromptTokens: 10_339,
		PromptTokensDetails: &PromptTokensDetails{
			CachedTokens: 10_318,
		},
	}
	// aliasID/provider name an Anthropic model on purpose: the function must
	// ignore that and decide from the wire shape alone.
	got := NormalizeCacheUsage(usage, "hive-claude-via-openrouter", "openrouter")

	if got.FreshInputTokens != 21 {
		t.Errorf("FreshInputTokens = %d, want 21 (10339 - 10318, inclusive subtract): "+
			"a model-name based shape selection would have added instead and produced a much larger, wrong figure", got.FreshInputTokens)
	}
}

// TestNormalizeCacheUsageNegativeFreshClampsAndWarnsLoudly covers the
// inclusive-shape failure mode: if cache tokens ever exceed prompt_tokens
// (the upstream shape changed underneath us), fresh must clamp to zero
// rather than go negative and silently discount the charge, and the clamp
// must be loud (a WARN naming the alias), never silent, per D-034 and the
// magnitude-guard contract.
func TestNormalizeCacheUsageNegativeFreshClampsAndWarnsLoudly(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	usage := &UsageResponse{
		PromptTokens: 100,
		PromptTokensDetails: &PromptTokensDetails{
			CachedTokens: 150, // exceeds prompt_tokens: shape must have changed
		},
	}
	got := NormalizeCacheUsage(usage, "hive-fast", "groq")

	if got.FreshInputTokens != 0 {
		t.Errorf("FreshInputTokens = %d, want 0 (clamped)", got.FreshInputTokens)
	}
	if !bytes.Contains(buf.Bytes(), []byte("BUG:")) || !bytes.Contains(buf.Bytes(), []byte("hive-fast")) {
		t.Errorf("expected a loud BUG WARN naming the alias, got log output: %q", buf.String())
	}
}

// TestNormalizeCacheUsageNilUsageIsZeroValue covers the no-usage-block case:
// nothing to normalize, no panic, all-zero result.
func TestNormalizeCacheUsageNilUsageIsZeroValue(t *testing.T) {
	got := NormalizeCacheUsage(nil, "hive-fast", "groq")
	if got != (CacheUsage{}) {
		t.Errorf("expected zero-value CacheUsage for nil usage, got %+v", got)
	}
}

// TestNormalizeCacheUsageNegativeCacheComponentClampsLoudlyInclusiveShape
// covers the review-found gap: a negative cacheRead or cacheWrite from a
// malformed upstream must not silently inflate fresh (subtracting a negative
// number adds); each component is validated and clamped, loudly, BEFORE
// fresh is derived, so the partition invariant survives.
func TestNormalizeCacheUsageNegativeCacheComponentClampsLoudlyInclusiveShape(t *testing.T) {
	t.Run("negative cache read", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		usage := &UsageResponse{
			PromptTokens: 100,
			PromptTokensDetails: &PromptTokensDetails{
				CachedTokens: -10, // malformed upstream
			},
		}
		got := NormalizeCacheUsage(usage, "hive-fast", "groq")

		if got.CacheReadTokens != 0 {
			t.Errorf("CacheReadTokens = %d, want 0 (clamped)", got.CacheReadTokens)
		}
		// Without the fix, fresh would be 100 - (-10) - 0 = 110: inflated,
		// past the wire total, and never caught by the fresh<0 clamp.
		if got.FreshInputTokens != 100 {
			t.Errorf("FreshInputTokens = %d, want 100 (unaffected by the clamped-away negative component)", got.FreshInputTokens)
		}
		if sum := got.FreshInputTokens + got.CacheReadTokens + got.CacheWriteTokens; sum != usage.PromptTokens {
			t.Errorf("partition invariant broke: fresh+read+write = %d, want prompt_tokens = %d", sum, usage.PromptTokens)
		}
		if !bytes.Contains(buf.Bytes(), []byte("BUG:")) || !bytes.Contains(buf.Bytes(), []byte("hive-fast")) {
			t.Errorf("expected a loud BUG line naming the alias, got: %q", buf.String())
		}
	})

	t.Run("negative cache write", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		usage := &UsageResponse{
			PromptTokens: 100,
			PromptTokensDetails: &PromptTokensDetails{
				CacheWriteTokens: -5,
			},
		}
		got := NormalizeCacheUsage(usage, "hive-fast", "groq")

		if got.CacheWriteTokens != 0 {
			t.Errorf("CacheWriteTokens = %d, want 0 (clamped)", got.CacheWriteTokens)
		}
		if got.FreshInputTokens != 100 {
			t.Errorf("FreshInputTokens = %d, want 100", got.FreshInputTokens)
		}
		if !bytes.Contains(buf.Bytes(), []byte("BUG:")) {
			t.Errorf("expected a loud BUG line, got: %q", buf.String())
		}
	})
}

// TestNormalizeCacheUsageNegativeCacheComponentClampsLoudlyExclusiveShape is
// the same defense on the EXCLUSIVE (Anthropic-native) branch.
func TestNormalizeCacheUsageNegativeCacheComponentClampsLoudlyExclusiveShape(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	negativeRead := int64(-50)
	usage := &UsageResponse{
		PromptTokens:         5_000,
		CacheReadInputTokens: &negativeRead,
	}
	got := NormalizeCacheUsage(usage, "claude-direct", "anthropic")

	if got.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0 (clamped)", got.CacheReadTokens)
	}
	if got.FreshInputTokens != 5_000 {
		t.Errorf("FreshInputTokens = %d, want 5000 (unaffected, exclusive shape never derives fresh from cache components)", got.FreshInputTokens)
	}
	if !bytes.Contains(buf.Bytes(), []byte("BUG:")) || !bytes.Contains(buf.Bytes(), []byte("claude-direct")) {
		t.Errorf("expected a loud BUG line naming the alias, got: %q", buf.String())
	}
}
