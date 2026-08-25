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

// clampNonNegative is the one clamp-and-alarm shape every money-path boundary
// in this file (and in CreditsForTokens, which shares it) uses for external
// input: a negative quantity from a provider is a malformed response or a
// shape regression, never a legitimate value, so it is clamped to zero AND
// logged loudly, naming the alias, the provider and which field tripped it.
// Never silent: a silent clamp here is exactly the gap PR #1157 review found
// (a corrupted negative cache component would otherwise inflate `fresh`
// instead of tripping any alarm, and then get silently zeroed two frames
// down in CreditsForTokens with no trace anywhere).
func clampNonNegative(value int64, aliasID, provider, field string) int64 {
	if value >= 0 {
		return value
	}
	log.Printf("inference: BUG: negative %s alias=%s provider=%s value=%d: upstream reported a negative token count, clamping to zero",
		field, aliasID, provider, value)
	return 0
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
// Both cacheRead and cacheWrite are validated non-negative BEFORE fresh is
// derived from them, in both branches. A negative component from a malformed
// upstream response would otherwise inflate fresh (subtracting a negative
// number adds), sail straight past the aggregate fresh<0 clamp below, and
// only get caught, silently, two frames down in CreditsForTokens -- by which
// point the partition invariant (fresh + cacheRead + cacheWrite == total)
// this file's own tests assert has already broken.
//
// aliasID and provider are for the WARN/BUG log lines only, naming which
// route a usage object came from when the arithmetic needed clamping; neither
// is ever customer-facing.
func NormalizeCacheUsage(usage *UsageResponse, aliasID, provider string) CacheUsage {
	if usage == nil {
		return CacheUsage{}
	}

	if usage.CacheReadInputTokens != nil || usage.CacheCreationInputTokens != nil {
		// EXCLUSIVE shape: prompt_tokens already excludes both cache fields,
		// so it IS the fresh count. Add, never subtract.
		cacheRead := clampNonNegative(derefPrice(usage.CacheReadInputTokens), aliasID, provider, "cache_read_input_tokens")
		cacheWrite := clampNonNegative(derefPrice(usage.CacheCreationInputTokens), aliasID, provider, "cache_creation_input_tokens")
		return CacheUsage{
			FreshInputTokens: usage.PromptTokens,
			CacheReadTokens:  cacheRead,
			CacheWriteTokens: cacheWrite,
		}
	}

	// INCLUSIVE shape: prompt_tokens counts the cached subset too. Subtract,
	// never add.
	var cacheRead, cacheWrite int64
	if usage.PromptTokensDetails != nil {
		cacheRead = usage.PromptTokensDetails.CachedTokens
		cacheWrite = usage.PromptTokensDetails.CacheWriteTokens
	}
	cacheRead = clampNonNegative(cacheRead, aliasID, provider, "cached_tokens")
	cacheWrite = clampNonNegative(cacheWrite, aliasID, provider, "cache_write_tokens")
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
