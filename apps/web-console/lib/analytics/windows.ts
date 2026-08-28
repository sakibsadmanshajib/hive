/**
 * Time-window presets the analytics overview tab understands, shared
 * between the fetch layer (`overview-fetch.ts`, which endpoint calls are
 * even legal for a given window) and the pure derivation layer
 * (`cache-metrics.ts`, which tiles get a delta/sparkline versus a plain
 * "unsupported" note). No I/O here, just the enum membership and math both
 * layers need to agree on.
 *
 * Two distinct window sets because two distinct backend handlers parse
 * `window` independently and recognize different enums:
 *   - parseAnalyticsFilter (apps/control-plane/internal/usage/http.go),
 *     backing usage/spend/errors summaries: 24h, 7d, 30d, 90d. An
 *     unrecognized value silently falls back to its own 7d default rather
 *     than 400ing, so ANALYTICS_WINDOW_SPAN_MS is exhaustive and every
 *     caller checks membership before relying on the value.
 *   - parseListEventsFilter, backing usage-events (the cache-hit sample):
 *     1h, 24h, 7d, 30d. No 90d.
 */
export const ANALYTICS_WINDOW_SPAN_MS: Readonly<Record<string, number>> = {
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
  "90d": 90 * 24 * 60 * 60 * 1000,
};

export const EVENT_SAMPLE_WINDOWS: ReadonlyArray<string> = [
  "1h",
  "24h",
  "7d",
  "30d",
];

export const EVENT_WINDOW_SPAN_MS: Readonly<Record<string, number>> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
};

// The usage-events endpoint caps a page at 100 rows (maxEventsPageLimit), so
// the cache sample is the most recent 100 requests in the window and every
// tile built from it says so.
export const CACHE_SAMPLE_LIMIT = 100;

// How many points a tile sparkline draws. Fixed and small: this is a shape
// indicator next to a headline number, not a chart anyone reads axis values
// off of.
export const SPARKLINE_BUCKETS = 8;

// Top API keys panel caps at this many rows, matching the reference's own
// "Top API Keys" panel width.
export const TOP_KEYS_LIMIT = 5;

/**
 * Own-property-only lookup into a span map, safe against a caller-controlled
 * key that happens to name an inherited Object.prototype member.
 *
 * `window` reaches this code straight off the URL query string with no
 * allowlist (`const timeWindow = params.window ?? "7d"` in the analytics
 * page), so both a plain `spanMap[window]` bracket lookup and a `window in
 * spanMap` membership check are reachable with `window` equal to
 * `"toString"`, `"constructor"`, `"valueOf"`, `"hasOwnProperty"`, or
 * `"__proto__"`. All five walk the prototype chain and resolve to an
 * inherited, always-truthy function rather than undefined, which used to
 * defeat this file's own `if (!spanMs) return null` guard: `new
 * Date(now.getTime() - <function>)` is an Invalid Date, and
 * `.toISOString()` on one throws a RangeError with no try/catch anywhere
 * between here and the page's own render call, which took the whole
 * overview tab down with a 500. Live-reproduced with `?window=toString`
 * (docs/proof/analytics-overview-parity-2026-08-28/capture-log.txt) before
 * this function existed. `Object.prototype.hasOwnProperty.call` only ever
 * matches a key the map's own literal actually declared.
 */
function windowSpanMs(
  spanMap: Readonly<Record<string, number>>,
  window: string,
): number | null {
  if (!Object.prototype.hasOwnProperty.call(spanMap, window)) {
    return null;
  }
  return spanMap[window];
}

/**
 * True when `window` is an own key of `spanMap` -- the safe replacement for
 * `window in spanMap`, which a caller-controlled `window` can defeat the
 * same way described on windowSpanMs above.
 */
export function hasWindowSpan(
  spanMap: Readonly<Record<string, number>>,
  window: string,
): boolean {
  return windowSpanMs(spanMap, window) !== null;
}

/**
 * Bounds of the equal-length period immediately before the one `window`
 * names, as explicit ISO8601 from/to. Null when `window` is not one of the
 * preset values `spanMap` recognizes (a custom range, or an unknown string),
 * since there is no principled "prior period" for either of those.
 */
export function priorPeriodBounds(
  spanMap: Readonly<Record<string, number>>,
  window: string,
  now: Date,
): { from: string; to: string } | null {
  const spanMs = windowSpanMs(spanMap, window);
  if (!spanMs) {
    return null;
  }
  const to = new Date(now.getTime() - spanMs);
  const from = new Date(to.getTime() - spanMs);
  return { from: from.toISOString(), to: to.toISOString() };
}
