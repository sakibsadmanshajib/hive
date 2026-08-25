package inference

import "log"

// CacheUsage partitions one request's prompt tokens into fresh, cache-read
// and cache-write quantities, so CreditsForTokens can price each component
// at its own rate instead of billing every prompt token at the flat input
// rate (audit: prompt-cache billing gap, vault
// spec-2026-08-25-cache-aware-billing.md).
//
// FreshInputTokens + CacheReadTokens + CacheWriteTokens always reconstructs
// the request's true total prompt tokens exactly, for either provider
// convention NormalizeCacheUsage understands.
type CacheUsage struct {
	FreshInputTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// NormalizeCacheUsage derives CacheUsage from one upstream usage object.
//
// Two provider conventions reach this function under overlapping
// OpenAI-compatible field names, with OPPOSITE arithmetic. Inverting them is
// the single most common bug in this feature:
//
//   - INCLUSIVE (OpenAI's own convention, and OpenRouter/LiteLLM's as well
//     even when the underlying model is Anthropic -- OpenRouter itself
//     normalizes a Claude response to this shape before Hive ever sees it,
//     so branching on model family here would be wrong): PromptTokens
//     already counts the cached subset. fresh = prompt_tokens - cached_tokens
//   - cache_write_tokens.
//   - EXCLUSIVE (Anthropic's own native API convention, reachable only
//     through a future direct, non-LiteLLM dispatch -- nothing in today's
//     dispatch path talks to api.anthropic.com directly): PromptTokens
//     already excludes both cache fields. fresh = prompt_tokens, unchanged.
//
// The choice is made on the WIRE SHAPE actually decoded, never on the model
// name: usage.CacheReadInputTokens or usage.CacheCreationInputTokens being
// non-nil (even a present zero) is Anthropic's own field spelling, and is
// the only signal that this usage object used the exclusive convention.
//
// aliasID and provider are for the WARN log line only, naming which route a
// usage object came from when the arithmetic needed clamping; neither is
// ever customer-facing.
func NormalizeCacheUsage(usage *UsageResponse, aliasID, provider string) CacheUsage {
	if usage == nil {
		return CacheUsage{}
	}

	if usage.CacheReadInputTokens != nil || usage.CacheCreationInputTokens != nil {
		// EXCLUSIVE shape: prompt_tokens already excludes both cache fields,
		// so it IS the fresh count. Add, never subtract.
		return CacheUsage{
			FreshInputTokens: usage.PromptTokens,
			CacheReadTokens:  derefPrice(usage.CacheReadInputTokens),
			CacheWriteTokens: derefPrice(usage.CacheCreationInputTokens),
		}
	}

	// INCLUSIVE shape: prompt_tokens counts the cached subset too. Subtract,
	// never add.
	var cacheRead, cacheWrite int64
	if usage.PromptTokensDetails != nil {
		cacheRead = usage.PromptTokensDetails.CachedTokens
		cacheWrite = usage.PromptTokensDetails.CacheWriteTokens
	}
	fresh := usage.PromptTokens - cacheRead - cacheWrite
	if fresh < 0 {
		// A negative fresh count means prompt_tokens did not actually include
		// the cache subset this time, i.e. the upstream's shape changed
		// underneath us. Clamp and say so loudly rather than let it silently
		// subtract from the charge (D-034): the trap here runs the other way
		// too (adding on an already-exclusive response nearly doubles the
		// charge), which is exactly why this is a clamp-and-alarm, not a
		// silent clamp.
		log.Printf("inference: BUG: negative fresh input tokens alias=%s provider=%s prompt_tokens=%d cache_read=%d cache_write=%d: upstream usage shape may have changed, clamping to zero",
			aliasID, provider, usage.PromptTokens, cacheRead, cacheWrite)
		fresh = 0
	}
	return CacheUsage{FreshInputTokens: fresh, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite}
}
