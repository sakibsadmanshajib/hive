/**
 * Cache metrics the console derives from rows it already fetches.
 *
 * The control-plane analytics aggregate (`GetUsageSummary` in
 * apps/control-plane/internal/usage/repository.go) sums input tokens, output
 * tokens, credits and request count only. It has no `SUM(cache_read_tokens)`,
 * so there is no server-side cache hit rate to read. Rather than invent one,
 * these functions derive it from the same `usage_events` rows the request log
 * renders, and every caller is required to state which sample the number
 * covers. A cache hit rate whose sample is unstated is worse than no tile.
 */

/** The subset of a usage event these derivations read. */
export interface CacheTokenRow {
  input_tokens: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface CacheHitRate {
  /** Cache-read tokens observed across the sample. */
  cachedTokens: number;
  /** Total prompt tokens across the sample, cached subset included. */
  promptTokens: number;
  /** cachedTokens / promptTokens, or null when the sample carries no prompt tokens. */
  rate: number | null;
  /** How many rows the sample actually covered. */
  sampleSize: number;
}

/**
 * Cache hit rate as cached prompt tokens over total prompt tokens, which is
 * the definition OpenRouter's own tile uses.
 *
 * The arithmetic rests on one fact about what is stored.
 * `usage_events.input_tokens` holds the RAW upstream `prompt_tokens` figure,
 * not the fresh count that billing prices: see the `UsageAccumulator.InputTokens`
 * comment in apps/edge-api/internal/inference/stream.go and the assignment in
 * orchestrator.go's `recordCompletedEvent`. Under the INCLUSIVE convention,
 * which is the only one any dispatch path in the product reaches today because
 * LiteLLM normalizes even an Anthropic response to it (see `NormalizeCacheUsage`
 * in cache_usage.go), `prompt_tokens` already counts the cached subset. So
 * `input_tokens` is the denominator and `cache_read_tokens` is the numerator.
 *
 * The EXCLUSIVE convention reports `prompt_tokens` with both cache components
 * already removed, and nothing in a stored row marks which shape produced it.
 * So this guards on the arithmetic rather than on a flag that does not exist:
 * under INCLUSIVE, cache read alone can never exceed `input_tokens`, because
 * it is a subset of it. A row where cache read does exceed input can only
 * have come from the exclusive shape.
 *
 * That check deliberately reads `cache_read_tokens` alone, never
 * `cache_write_tokens`. Per decision D-056 every `cache_write_tokens` value
 * stored before PR #1157 deployed is a bug artifact rather than a measured
 * quantity, and a stored row carries no marker saying which side of that
 * deploy it fell on, so it can never be trusted, in either direction: not as
 * the signal that decides which shape a row is, and not as an addend in the
 * prompt-token total once a shape is picked. An artifact large enough on its
 * own to push `cache_read_tokens + cache_write_tokens` past `input_tokens`
 * used to flip a genuinely inclusive row to the exclusive branch and inflate
 * the denominator with garbage, silently lowering the displayed hit rate.
 * Cache WRITE is excluded from both the shape check and the prompt-token sum
 * for exactly that reason; only cache READ, which carries no such artifact
 * history, ever moves this arithmetic.
 */
export function deriveCacheHitRate(
  rows: ReadonlyArray<CacheTokenRow>,
): CacheHitRate {
  let cachedTokens = 0;
  let promptTokens = 0;

  for (const row of rows) {
    const cacheRead = row.cache_read_tokens ?? 0;
    const input = row.input_tokens;
    const exclusiveShape = cacheRead > input;

    promptTokens += exclusiveShape ? input + cacheRead : input;
    cachedTokens += cacheRead;
  }

  return {
    cachedTokens,
    promptTokens,
    rate: promptTokens > 0 ? cachedTokens / promptTokens : null,
    sampleSize: rows.length,
  };
}

/**
 * Blended effective price: the credits actually charged across a window
 * divided by the tokens that window moved, restated per million tokens so it
 * sits in the same unit as the catalog's `Input / 1M` and `Output / 1M`
 * columns.
 *
 * "Effective" is the whole point of the number. It is what the account paid
 * after cache reads were priced at the cache rate, so it lands below the
 * listed input rate exactly when caching is working. Both inputs come from
 * the server-side aggregate, so this covers the full window rather than a
 * sample.
 *
 * Returns null on a zero-token window: dividing by it would print Infinity,
 * and printing 0 would claim the account was served for free.
 */
export function deriveBlendedCreditsPerMillion(
  totalCreditsSpent: number,
  totalTokens: number,
): number | null {
  if (totalTokens <= 0) {
    return null;
  }
  return (totalCreditsSpent / totalTokens) * 1_000_000;
}
