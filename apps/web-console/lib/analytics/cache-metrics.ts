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
 *
 * No I/O in this file, by design: everything here is a pure function of data
 * already fetched by lib/analytics/overview-fetch.ts. Type-only imports from
 * control-plane/client are fine (they cost nothing at runtime), and the one
 * runtime value this file compares against is imported from
 * control-plane/contract, which is dependency-free precisely so that import
 * does not drag the server graph of client.ts in here (issue #1347); an actual
 * fetch call here would not be.
 */
import type {
  ApiKey,
  SpendSummaryRow,
  UsageEventRow,
  UsageSummaryRow,
} from "@/lib/control-plane/client";
import { UNATTRIBUTED_GROUP_KEY } from "@/lib/control-plane/contract";
import {
  ANALYTICS_WINDOW_SPAN_MS,
  EVENT_SAMPLE_WINDOWS,
  SPARKLINE_BUCKETS,
  TOP_KEYS_LIMIT,
  hasWindowSpan,
} from "./windows";

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

export interface PeriodDelta {
  /**
   * Percent change of `current` versus `previous`, e.g. 50 for a 50% rise.
   * Null in the three cases below where no finite percentage exists, each of
   * which the flags distinguish so the UI never renders two of them as the
   * same sentence.
   */
  percent: number | null;
  direction: "up" | "down" | "flat";
  /**
   * True when the prior-period fetch itself failed. "The prior period had no
   * requests" and "we could not ask" are different facts and must render
   * different text (repo convention: lib/format/model-pricing.ts never
   * collapses "explicit zero" and "unknown" into the same label). Absent,
   * not false, otherwise, so callers that never pass the flag do not have to
   * check it.
   */
  unavailable?: boolean;
  /**
   * True when the prior period is a MEASURED zero and the current period is
   * not: a genuine, observed move off zero, which simply has no finite
   * percent change. Separate from a bare `percent: null` because "no prior
   * figure exists" and "the prior figure was zero" are as different as the
   * two above. An account that served plenty of requests last period and hit
   * cache on none of them has a real 0% prior cache hit rate, not an absent
   * one, and a rise off it is a rise.
   */
  fromZero?: boolean;
}

/**
 * Percent change of a tile's current-period total versus the equal-length
 * period immediately before it. Both totals must come from the same full
 * server-side aggregate the tile itself renders (never a bounded sample),
 * so the percentage is exact rather than a sample-derived estimate.
 *
 * The three not-a-percentage cases stay separate, because the caller renders
 * a different sentence for each and collapsing any two of them states
 * something about the account that was never measured:
 *
 *   - `previousUnavailable` is the caller's signal that the prior-period
 *     fetch failed (network error, 5xx, timeout). It must never be reached
 *     by coercing a failed fetch to `0` or `[]` and reducing that, see
 *     fetchPreviousUsage in lib/analytics/overview-fetch.ts.
 *   - `previous: null` means no prior figure exists at all, which is not the
 *     same as a prior figure of zero. A blended price over a prior period
 *     that served no tokens is undefined, not zero credits per million.
 *   - `previous` of exactly zero is a real, measured zero. For a count that
 *     is an account that ran nothing last period; for a RATE it is an
 *     account that measured 0%, and 0% to 20% is a genuine rise rather than
 *     an absence of prior data. That case returns `fromZero` with a real
 *     direction and no percentage, since dividing by zero yields none.
 */
export function derivePeriodDelta(
  current: number,
  previous: number | null,
  previousUnavailable = false,
): PeriodDelta {
  if (previousUnavailable) {
    return { percent: null, direction: "flat", unavailable: true };
  }
  if (previous === null) {
    return { percent: null, direction: "flat" };
  }
  if (previous <= 0) {
    if (current === previous) {
      return { percent: 0, direction: "flat" };
    }
    return {
      percent: null,
      direction: current > previous ? "up" : "down",
      fromZero: true,
    };
  }
  const percent = ((current - previous) / previous) * 100;
  return {
    percent,
    direction: percent > 0 ? "up" : percent < 0 ? "down" : "flat",
  };
}

/**
 * Splits `rows` into `bucketCount` equal-width time buckets spanning
 * [windowStart, windowEnd], ordered oldest bucket first. Used to build the
 * tile sparklines from the same bounded usage_events sample the cache-hit
 * tile already fetches (see EVENT_SAMPLE_WINDOWS in the analytics page): a
 * trend across a sample, not a claim about the full window, exactly the
 * honesty contract deriveCacheHitRate documents above.
 *
 * A row whose timestamp cannot be parsed is dropped rather than thrown on.
 * A row landing exactly on windowEnd clamps into the last bucket instead of
 * spilling past the array. A zero-span window (or reversed bounds) returns
 * empty buckets rather than dividing by zero.
 */
export function bucketByTime<T extends { created_at: string }>(
  rows: ReadonlyArray<T>,
  bucketCount: number,
  windowStart: Date,
  windowEnd: Date,
): T[][] {
  const buckets: T[][] = Array.from({ length: bucketCount }, () => []);
  const startMs = windowStart.getTime();
  const endMs = windowEnd.getTime();
  const spanMs = endMs - startMs;
  if (spanMs <= 0) {
    return buckets;
  }

  for (const row of rows) {
    const rowMs = new Date(row.created_at).getTime();
    if (Number.isNaN(rowMs)) {
      continue;
    }
    const clampedMs = Math.min(Math.max(rowMs, startMs), endMs);
    const index = Math.min(
      bucketCount - 1,
      Math.floor(((clampedMs - startMs) / spanMs) * bucketCount),
    );
    buckets[index].push(row);
  }

  return buckets;
}

/**
 * Earliest and latest timestamp actually present in `rows`, or null on an
 * empty or all-unparseable sample.
 *
 * This exists because of a bucketing bug: `ListEvents`
 * (apps/control-plane/internal/usage/repository.go) orders `created_at DESC`
 * and pages, so a bounded sample (the 100-row cap this file's callers all
 * live under) is the MOST RECENT rows in the window, not an even sample
 * across it. Handing that sample to bucketByTime with bounds spanning the
 * full nominal window (e.g. "the last 7 days") starves every early bucket
 * on any window where real traffic exceeds the page cap -- a 7d or 30d
 * active account, easily -- and draws a sparkline shaped like "a surge
 * just happened" that has nothing to do with the account's real trend, it
 * is purely an artifact of where the page boundary fell. Bucketing over the
 * sample's own real span instead makes every bucket boundary describe where
 * the fetched rows actually sit in time, sample or no sample.
 */
export function sampleTimeSpan(
  rows: ReadonlyArray<{ created_at: string }>,
): { start: Date; end: Date } | null {
  let minMs = Infinity;
  let maxMs = -Infinity;
  for (const row of rows) {
    const t = new Date(row.created_at).getTime();
    if (Number.isNaN(t)) {
      continue;
    }
    if (t < minMs) minMs = t;
    if (t > maxMs) maxMs = t;
  }
  if (!Number.isFinite(minMs) || !Number.isFinite(maxMs)) {
    return null;
  }
  return { start: new Date(minMs), end: new Date(maxMs) };
}

/**
 * Explains why a delta or sparkline is missing on the four "plain" overview
 * tiles (requests, input tokens, output tokens, total spend), which share
 * one prior-period fetch and one cache sample and so share one window
 * eligibility check. Undefined when both are supported, meaning the caller
 * renders no note at all. This mirrors deriveCacheHitNote below, which
 * already made exactly this distinction for the cache-hit tile alone; every
 * other tile silently showing nothing on an unsupported window (a custom
 * range, which only a hand-typed query string produces now that the Custom
 * control is gone (issue #1338), or
 * 90d for the sparkline half) was the gap this closes.
 */
function overviewWindowNote(
  hasDeltaWindow: boolean,
  hasSparklineWindow: boolean,
): string | undefined {
  if (hasDeltaWindow && hasSparklineWindow) {
    return undefined;
  }
  if (!hasDeltaWindow && !hasSparklineWindow) {
    return "No comparison or trend for this window. Pick 24h, 7d or 30d.";
  }
  if (!hasDeltaWindow) {
    return "No comparison for this window. Pick 24h, 7d, 30d or 90d.";
  }
  return "No trend sample for this window. Pick 1h, 24h, 7d or 30d.";
}

/**
 * Same "state the sample" contract as deriveCacheHitRate: text for both the
 * cache-hit tile's own note and the cached-vs-uncached panel's empty state
 * (they describe the same sample, so they share the same explanation).
 */
function deriveCacheHitNote(
  timeWindow: string,
  cacheHitRate: CacheHitRate | null,
  cacheSampleTruncated: boolean,
): string {
  if (!EVENT_SAMPLE_WINDOWS.includes(timeWindow)) {
    return "No sample for this window. Pick 1h, 24h, 7d or 30d.";
  }
  if (!cacheHitRate) {
    return "Request sample unavailable.";
  }
  if (cacheHitRate.sampleSize === 0) {
    return "No requests in this window.";
  }
  if (cacheHitRate.promptTokens === 0) {
    return `No prompt tokens across the last ${cacheHitRate.sampleSize} requests.`;
  }
  return cacheSampleTruncated
    ? `Cached prompt tokens over the last ${cacheHitRate.sampleSize} requests, the most this window returns in one page.`
    : `Cached prompt tokens over all ${cacheHitRate.sampleSize} requests in this window.`;
}

export interface TopKeyRow {
  id: string;
  label: string;
  suffix: string;
  credits: number;
}

export type BlendedNoteKind = "no-tokens" | "window-unsupported" | "ok";

export interface OverviewDeriveInput {
  timeWindow: string;
  /** Current-period rows, already fetched for the tab. */
  usage: ReadonlyArray<UsageSummaryRow>;
  /** Null means the prior-period fetch itself failed; `[]` is a real empty result. */
  previousUsage: ReadonlyArray<UsageSummaryRow> | null;
  /** Null means no sample: unsupported window, or the fetch failed. */
  cacheSample: ReadonlyArray<UsageEventRow> | null;
  cacheSampleTruncated: boolean;
  previousCacheSample: ReadonlyArray<UsageEventRow> | null;
  /** Null means the fetch itself failed; `{spend:[],keys:[]}` is a real empty result. */
  topKeys: { spend: ReadonlyArray<SpendSummaryRow>; keys: ReadonlyArray<ApiKey> } | null;
}

export interface OverviewTiles {
  totalRequests: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  totalCreditsSpent: number;

  requestsDelta?: PeriodDelta;
  inputTokensDelta?: PeriodDelta;
  outputTokensDelta?: PeriodDelta;
  creditsDelta?: PeriodDelta;
  cacheHitDelta?: PeriodDelta;
  blendedDelta?: PeriodDelta;

  /** Set on requests/input/output/credits when their window supports neither delta nor sparkline. */
  windowUnsupportedNote?: string;

  requestsSparkline?: number[];
  inputTokensSparkline?: number[];
  outputTokensSparkline?: number[];
  creditsSparkline?: number[];
  cacheHitSparkline?: Array<number | null>;
  /** Visible, non-decorative caption for every sparkline above: what sample they trend across. */
  sparklineCaption?: string;

  cacheHitRate: number | null;
  cacheHitNote: string;

  blendedCreditsPerMillion: number | null;
  blendedNoteKind: BlendedNoteKind;

  cachedTokens: number;
  uncachedTokens: number;
  hasCacheSplit: boolean;

  topKeys: TopKeyRow[];
  topKeysFailed: boolean;
}

/**
 * Turns the raw fetch bundle from lib/analytics/overview-fetch.ts into
 * everything the overview tab's tiles render: totals, deltas, sparklines,
 * notes, the cache split and the top-keys ranking. Pure and synchronous, so
 * every branch (a failed prior period, an unsupported window, a genuinely
 * empty result) is directly testable without rendering anything.
 */
export function deriveOverviewTiles(input: OverviewDeriveInput): OverviewTiles {
  const {
    timeWindow,
    usage,
    previousUsage,
    cacheSample,
    cacheSampleTruncated,
    previousCacheSample,
    topKeys,
  } = input;

  const totalRequests = usage.reduce((sum, r) => sum + r.request_count, 0);
  const totalInputTokens = usage.reduce((sum, r) => sum + r.total_input_tokens, 0);
  const totalOutputTokens = usage.reduce((sum, r) => sum + r.total_output_tokens, 0);
  const totalCreditsSpent = usage.reduce((sum, r) => sum + r.total_credits_spent, 0);

  const previousUsageFailed = previousUsage === null;
  const previousUsageRows = previousUsage ?? [];
  const previousTotalRequests = previousUsageRows.reduce((sum, r) => sum + r.request_count, 0);
  const previousTotalInputTokens = previousUsageRows.reduce((sum, r) => sum + r.total_input_tokens, 0);
  const previousTotalOutputTokens = previousUsageRows.reduce((sum, r) => sum + r.total_output_tokens, 0);
  const previousTotalTokens = previousTotalInputTokens + previousTotalOutputTokens;
  const previousTotalCreditsSpent = previousUsageRows.reduce((sum, r) => sum + r.total_credits_spent, 0);

  // Not `timeWindow in ANALYTICS_WINDOW_SPAN_MS`: `in` walks the prototype
  // chain, and `timeWindow` is unvalidated user input (the page reads it
  // straight off the URL query string). hasWindowSpan is the own-property
  // check that doesn't resolve "toString" et al. to an inherited, always
  // truthy method -- see its doc comment in windows.ts for the live-captured
  // crash this replaces.
  const hasDeltaWindow = hasWindowSpan(ANALYTICS_WINDOW_SPAN_MS, timeWindow);
  const hasSparklineWindow = EVENT_SAMPLE_WINDOWS.includes(timeWindow);

  // previousUsageFailed rides the third argument rather than being folded
  // into the previous value as a null, so derivePeriodDelta can keep "the
  // fetch failed", "there is no prior figure" and "the prior figure was a
  // measured zero" apart.
  const requestsDelta = hasDeltaWindow
    ? derivePeriodDelta(totalRequests, previousTotalRequests, previousUsageFailed)
    : undefined;
  const inputTokensDelta = hasDeltaWindow
    ? derivePeriodDelta(totalInputTokens, previousTotalInputTokens, previousUsageFailed)
    : undefined;
  const outputTokensDelta = hasDeltaWindow
    ? derivePeriodDelta(totalOutputTokens, previousTotalOutputTokens, previousUsageFailed)
    : undefined;
  const creditsDelta = hasDeltaWindow
    ? derivePeriodDelta(totalCreditsSpent, previousTotalCreditsSpent, previousUsageFailed)
    : undefined;

  const windowUnsupportedNote = overviewWindowNote(hasDeltaWindow, hasSparklineWindow);

  const cacheHitRateResult = cacheSample ? deriveCacheHitRate(cacheSample) : null;
  const previousCacheHitRateResult = previousCacheSample
    ? deriveCacheHitRate(previousCacheSample)
    : null;
  const cacheHitDelta =
    cacheHitRateResult?.rate != null && previousCacheHitRateResult?.rate != null
      ? derivePeriodDelta(cacheHitRateResult.rate, previousCacheHitRateResult.rate)
      : undefined;
  const cacheHitNoteText = deriveCacheHitNote(timeWindow, cacheHitRateResult, cacheSampleTruncated);

  const blendedCreditsPerMillion = deriveBlendedCreditsPerMillion(
    totalCreditsSpent,
    totalInputTokens + totalOutputTokens,
  );
  // Null here carries its own real meaning, distinct from a failed fetch: no
  // prior tokens to price at all (deriveBlendedCreditsPerMillion's own
  // guard), which derivePeriodDelta now reads as "no prior figure exists"
  // and the tile renders as "No prior data". The failure signal travels
  // separately, in the third argument below. The `?? 0` that used to sit at
  // that call site is gone with it: it turned an unpriceable prior period
  // into a measured price of zero credits per million, a figure nothing ever
  // observed, and then compared this window against it.
  const previousBlendedCreditsPerMillion = deriveBlendedCreditsPerMillion(
    previousTotalCreditsSpent,
    previousTotalTokens,
  );
  const blendedNoteKind: BlendedNoteKind =
    blendedCreditsPerMillion === null
      ? "no-tokens"
      : !hasDeltaWindow
        ? "window-unsupported"
        : "ok";
  const blendedDelta =
    blendedCreditsPerMillion === null || !hasDeltaWindow
      ? undefined
      : derivePeriodDelta(
          blendedCreditsPerMillion,
          previousBlendedCreditsPerMillion,
          previousUsageFailed,
        );

  // Sparklines: bucket the CURRENT cache sample over its own real time
  // span (sampleTimeSpan), never the nominal window -- see that function's
  // doc comment for why the nominal window fabricates a shape on any
  // truncated sample.
  let requestsSparkline: number[] | undefined;
  let inputTokensSparkline: number[] | undefined;
  let outputTokensSparkline: number[] | undefined;
  let creditsSparkline: number[] | undefined;
  let cacheHitSparkline: Array<number | null> | undefined;
  let sparklineCaption: string | undefined;
  const span = cacheSample ? sampleTimeSpan(cacheSample) : null;
  // A sample whose rows all share one timestamp has no span, and bucketByTime
  // answers a zero span with eight empty buckets. Drawing those would put a
  // flat line at zero under a tile whose headline number is not zero, which is
  // the same fabrication this file exists to avoid, so a sample with no
  // duration gets no sparkline and no caption at all. One instant is not a
  // trend.
  const hasSpan = span !== null && span.end.getTime() !== span.start.getTime();
  if (cacheSample && cacheSample.length > 0 && span && hasSpan) {
    const buckets = bucketByTime(cacheSample, SPARKLINE_BUCKETS, span.start, span.end);
    requestsSparkline = buckets.map((bucket) => bucket.length);
    inputTokensSparkline = buckets.map((bucket) =>
      bucket.reduce((sum, row) => sum + row.input_tokens, 0),
    );
    outputTokensSparkline = buckets.map((bucket) =>
      bucket.reduce((sum, row) => sum + row.output_tokens, 0),
    );
    creditsSparkline = buckets.map((bucket) =>
      bucket.reduce((sum, row) => sum + Math.max(0, -row.hive_credit_delta), 0),
    );
    cacheHitSparkline = buckets.map((bucket) => deriveCacheHitRate(bucket).rate);
    sparklineCaption = cacheSampleTruncated
      ? `Trend across the last ${cacheSample.length} requests`
      : `Trend across all ${cacheSample.length} requests this window`;
  }

  const uncachedTokens =
    cacheHitRateResult && cacheHitRateResult.promptTokens > cacheHitRateResult.cachedTokens
      ? cacheHitRateResult.promptTokens - cacheHitRateResult.cachedTokens
      : 0;
  const hasCacheSplit = Boolean(
    cacheHitRateResult && cacheHitRateResult.sampleSize > 0 && cacheHitRateResult.promptTokens > 0,
  );

  const topKeysFailed = topKeys === null;
  const topKeysRows = topKeys?.spend ?? [];
  const apiKeys = topKeys?.keys ?? [];
  const apiKeyById = new Map(apiKeys.map((key) => [key.id, key] as const));
  const topKeysList: TopKeyRow[] = [...topKeysRows]
    .sort((a, b) => b.total_credits - a.total_credits)
    .slice(0, TOP_KEYS_LIMIT)
    .map((row) => {
      // A NULL api_key_id groups here. The bucket mixes causes that the row
      // itself cannot tell apart (traffic that carried no key, an error
      // before a key was resolved, a key deleted under ON DELETE SET NULL),
      // so both the label and the suffix have to be true of all three. What
      // is certainly false for the first two is "Deleted key", which is how
      // the bucket rendered on the qa-tester workspace before this fix, whose
      // spend in that bucket was chat traffic that never carried a key.
      const unattributed = row.group_key === UNATTRIBUTED_GROUP_KEY;
      const key = unattributed ? undefined : apiKeyById.get(row.group_key);
      return {
        id: row.group_key,
        label: unattributed ? "Unattributed" : key ? key.nickname : "Deleted key",
        suffix: unattributed ? "no key on record" : (key?.redacted_suffix ?? row.group_key.slice(0, 8)),
        credits: row.total_credits,
      };
    });

  return {
    totalRequests,
    totalInputTokens,
    totalOutputTokens,
    totalCreditsSpent,
    requestsDelta,
    inputTokensDelta,
    outputTokensDelta,
    creditsDelta,
    cacheHitDelta,
    blendedDelta,
    windowUnsupportedNote,
    requestsSparkline,
    inputTokensSparkline,
    outputTokensSparkline,
    creditsSparkline,
    cacheHitSparkline,
    sparklineCaption,
    cacheHitRate: cacheHitRateResult?.rate ?? null,
    cacheHitNote: cacheHitNoteText,
    blendedCreditsPerMillion,
    blendedNoteKind,
    cachedTokens: cacheHitRateResult?.cachedTokens ?? 0,
    uncachedTokens,
    hasCacheSplit,
    topKeys: topKeysList,
    topKeysFailed,
  };
}
