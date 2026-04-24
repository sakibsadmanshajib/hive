package routing

type ProviderUsageInput struct {
	PromptTokens             int64
	CompletionTokens         int64
	PromptCachedTokens       int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
}

func NormalizeCacheUsage(input ProviderUsageInput) (cacheReadTokens int64, cacheWriteTokens int64) {
	cacheReadTokens = input.CacheReadInputTokens
	if cacheReadTokens == 0 && input.PromptCachedTokens > 0 {
		cacheReadTokens = input.PromptCachedTokens
	}

	cacheWriteTokens = input.CacheCreationInputTokens
	return cacheReadTokens, cacheWriteTokens
}
