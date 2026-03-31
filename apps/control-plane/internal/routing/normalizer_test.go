package routing

import "testing"

func TestNormalizeCacheUsageMapsExplicitReadAndWriteFields(t *testing.T) {
	cacheReadTokens, cacheWriteTokens := NormalizeCacheUsage(ProviderUsageInput{
		PromptTokens:             100,
		CompletionTokens:         50,
		PromptCachedTokens:       12,
		CacheReadInputTokens:     24,
		CacheCreationInputTokens: 36,
	})

	if cacheReadTokens != 24 {
		t.Fatalf("expected explicit cache read tokens 24, got %d", cacheReadTokens)
	}
	if cacheWriteTokens != 36 {
		t.Fatalf("expected explicit cache write tokens 36, got %d", cacheWriteTokens)
	}
}

func TestNormalizeCacheUsageFallsBackToPromptCachedTokens(t *testing.T) {
	cacheReadTokens, cacheWriteTokens := NormalizeCacheUsage(ProviderUsageInput{
		PromptTokens:       100,
		CompletionTokens:   50,
		PromptCachedTokens: 18,
	})

	if cacheReadTokens != 18 {
		t.Fatalf("expected prompt cached token fallback 18, got %d", cacheReadTokens)
	}
	if cacheWriteTokens != 0 {
		t.Fatalf("expected zero cache write tokens, got %d", cacheWriteTokens)
	}
}

func TestNormalizeCacheUsageDoesNotInventWriteTokens(t *testing.T) {
	_, cacheWriteTokens := NormalizeCacheUsage(ProviderUsageInput{
		PromptTokens:       100,
		CompletionTokens:   50,
		PromptCachedTokens: 45,
	})

	if cacheWriteTokens != 0 {
		t.Fatalf("expected zero cache write tokens without explicit cache creation tokens, got %d", cacheWriteTokens)
	}
}
