/**
 * Cache visibility across the console: the two catalog price columns, the two
 * request-log token columns, the CSV export, and the derivations behind the
 * analytics cache tiles.
 *
 * Every assertion here exists to catch one specific way this feature could
 * lie: a null price rendered as free, an unmeasured token count rendered as a
 * verified zero, an unknown price winning a cheapest-first sort, or a cache
 * hit rate computed against the wrong denominator.
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

import type {
  ApiKey,
  CatalogModel,
  SpendSummaryRow,
  UsageEventRow,
  UsageSummaryRow,
} from "@/lib/control-plane/client";
import { UNATTRIBUTED_GROUP_KEY } from "@/lib/control-plane/contract";
import {
  bucketByTime,
  deriveBlendedCreditsPerMillion,
  deriveCacheHitRate,
  deriveOverviewTiles,
  derivePeriodDelta,
  sampleTimeSpan,
} from "@/lib/analytics/cache-metrics";
import {
  ANALYTICS_WINDOW_SPAN_MS,
  hasWindowSpan,
  priorPeriodBounds,
} from "@/lib/analytics/windows";
import { ModelCatalogBrowser } from "@/components/catalog/model-catalog-browser";
import { ModelCatalogTable } from "@/components/catalog/model-catalog-table";
import { UsageLogsCsv } from "@/components/logs/usage-logs-csv";
import { UsageLogsTable } from "@/components/logs/usage-logs-table";
import { formatPercent } from "@/lib/format/credits";

function model(overrides: Partial<CatalogModel> = {}): CatalogModel {
  return {
    id: "hive-default",
    display_name: "Hive Default",
    summary: "Balanced default chat model.",
    capability_badges: ["chat"],
    pricing: {
      input_price_credits: 12_000_000,
      output_price_credits: 36_000_000,
      cache_read_price_credits: 1_200_000,
      cache_write_price_credits: 15_000_000,
      pricing_mode: "fixed",
    },
    lifecycle: "stable",
    ...overrides,
  };
}

function event(overrides: Partial<UsageEventRow> = {}): UsageEventRow {
  return {
    id: "e1",
    request_id: "r1",
    request_attempt_id: "a1",
    event_type: "completed",
    endpoint: "/v1/chat/completions",
    model_alias: "hive-default",
    status: "completed",
    input_tokens: 1000,
    output_tokens: 100,
    hive_credit_delta: -500,
    customer_tags: {},
    created_at: "2026-08-25T10:00:00Z",
    ...overrides,
  };
}

function usageRow(overrides: Partial<UsageSummaryRow> = {}): UsageSummaryRow {
  return {
    group_key: "hive-default",
    total_input_tokens: 100,
    total_output_tokens: 50,
    total_credits_spent: 30,
    request_count: 10,
    ...overrides,
  };
}

function spendRow(overrides: Partial<SpendSummaryRow> = {}): SpendSummaryRow {
  return {
    group_key: "key-1",
    total_credits: 100,
    entry_count: 5,
    ...overrides,
  };
}

function apiKey(overrides: Partial<ApiKey> = {}): ApiKey {
  return {
    id: "key-1",
    nickname: "Production",
    status: "active",
    redacted_suffix: "ab12",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    expires_at: null,
    last_used_at: null,
    expiration_summary: { kind: "never", label: "Never expires" },
    budget_summary: { kind: "none", label: "No budget" },
    allowlist_summary: { mode: "all", group_names: [], label: "All models" },
    // Required on ApiKey since the key spend column landed on main. Zero
    // here is a real measured zero, not an absent figure: these fixtures
    // exist for the top keys join, which reads credits off the spend rows
    // rather than off the key.
    spend_credits: 0,
    budget_limit_credits: null,
    ...overrides,
  };
}

describe("deriveCacheHitRate", () => {
  it("divides cache reads by total prompt tokens on the inclusive shape", () => {
    // input_tokens already counts the cached subset, which is the convention
    // every dispatch path in the product reaches today. 800 of 1000 prompt
    // tokens came from cache.
    const result = deriveCacheHitRate([
      event({ input_tokens: 1000, cache_read_tokens: 800 }),
    ]);

    expect(result.promptTokens).toBe(1000);
    expect(result.cachedTokens).toBe(800);
    expect(result.rate).toBe(0.8);
  });

  it("adds the cache components back when the row came from the exclusive shape", () => {
    // Anthropic's native convention reports prompt_tokens with both cache
    // components already removed, so cache read alone exceeds input_tokens.
    // Treating input_tokens as the denominator there would report 400%.
    const result = deriveCacheHitRate([
      event({
        input_tokens: 200,
        cache_read_tokens: 800,
        cache_write_tokens: 0,
      }),
    ]);

    expect(result.promptTokens).toBe(1000);
    expect(result.rate).toBe(0.8);
  });

  it("never reports a rate above 100 percent", () => {
    const result = deriveCacheHitRate([
      event({ input_tokens: 1, cache_read_tokens: 5_000 }),
      event({ input_tokens: 10, cache_read_tokens: 9 }),
    ]);

    expect(result.rate).not.toBeNull();
    expect(result.rate!).toBeLessThanOrEqual(1);
  });

  it("returns a null rate rather than zero on an empty sample", () => {
    // A zero here would claim a measured zero-percent hit rate over a window
    // in which nothing was measured at all.
    const result = deriveCacheHitRate([]);

    expect(result.rate).toBeNull();
    expect(result.sampleSize).toBe(0);
    expect(formatPercent(result.rate)).toBe("—");
  });

  it("counts a row with no cache fields as fully uncached", () => {
    const result = deriveCacheHitRate([
      event({ input_tokens: 500 }),
      event({ input_tokens: 500, cache_read_tokens: 500 }),
    ]);

    expect(result.rate).toBe(0.5);
  });

  it("does not let an artifact cache_write_tokens value flip a genuinely inclusive row to the exclusive shape", () => {
    // A pre-#1157 row can carry a garbage cache_write_tokens value large
    // enough on its own to exceed input_tokens, even though cache_read_tokens
    // (400) sits well inside input_tokens (1000) and the row is really the
    // ordinary inclusive shape: 400 of 1000 prompt tokens came from cache.
    // Letting the artifact drive shape detection used to add it to the
    // denominator too, diluting a true 40% hit rate down to under 1%.
    const result = deriveCacheHitRate([
      event({
        input_tokens: 1000,
        cache_read_tokens: 400,
        cache_write_tokens: 50_000,
      }),
    ]);

    expect(result.promptTokens).toBe(1000);
    expect(result.cachedTokens).toBe(400);
    expect(result.rate).toBe(0.4);
  });

  it("excludes cache writes from the numerator", () => {
    // D-056: a pre-#1157 cache_write_tokens value is a bug artifact, not a
    // measured quantity, so it must not move the hit rate.
    const withWrite = deriveCacheHitRate([
      event({
        input_tokens: 1000,
        cache_read_tokens: 400,
        cache_write_tokens: 300,
      }),
    ]);

    expect(withWrite.cachedTokens).toBe(400);
    expect(withWrite.rate).toBe(0.4);
  });
});

describe("deriveBlendedCreditsPerMillion", () => {
  it("restates credits per token as credits per million tokens", () => {
    expect(deriveBlendedCreditsPerMillion(2_000, 1_000)).toBe(2_000_000);
  });

  it("returns null on a zero-token window instead of zero or Infinity", () => {
    expect(deriveBlendedCreditsPerMillion(0, 0)).toBeNull();
    expect(deriveBlendedCreditsPerMillion(500, 0)).toBeNull();
  });
});

describe("derivePeriodDelta", () => {
  it("reports a positive percent change when current exceeds previous", () => {
    const result = derivePeriodDelta(150, 100);
    expect(result.percent).toBe(50);
    expect(result.direction).toBe("up");
  });

  it("reports a negative percent change when current is below previous", () => {
    const result = derivePeriodDelta(50, 100);
    expect(result.percent).toBe(-50);
    expect(result.direction).toBe("down");
  });

  it("reports flat when current equals previous", () => {
    const result = derivePeriodDelta(100, 100);
    expect(result.percent).toBe(0);
    expect(result.direction).toBe("flat");
  });

  it("reports a real move off a measured zero as a rise, never as missing prior data", () => {
    // A prior rate of 0 percent is measured, not absent, so a current 42
    // against it is a rise. No finite percentage exists for it, so fromZero
    // carries the direction instead.
    const result = derivePeriodDelta(42, 0);
    expect(result.percent).toBeNull();
    expect(result.fromZero).toBe(true);
    expect(result.direction).toBe("up");
    expect(result.unavailable).toBeFalsy();
  });

  it("treats a zero previous and zero current as flat at 0 percent, not an absence", () => {
    const result = derivePeriodDelta(0, 0);
    expect(result.percent).toBe(0);
    expect(result.direction).toBe("flat");
    expect(result.fromZero).toBeFalsy();
  });

  it("keeps a failed prior fetch, an absent prior figure and a measured zero as three distinct shapes", () => {
    // Tile text: Unavailable, No prior data, and an arrow off zero. Collapse
    // any two and the page states something the account never measured.
    const failed = derivePeriodDelta(42, 0, true);
    const absent = derivePeriodDelta(42, null);
    const measuredZero = derivePeriodDelta(42, 0);

    expect(failed.unavailable).toBe(true);
    expect(absent.unavailable).toBeFalsy();
    expect(absent.fromZero).toBeFalsy();
    expect(absent.percent).toBeNull();
    expect(measuredZero.fromZero).toBe(true);
    expect(measuredZero.unavailable).toBeFalsy();
  });
});

describe("bucketByTime", () => {
  const windowStart = new Date("2026-08-24T00:00:00Z");
  const windowEnd = new Date("2026-08-25T00:00:00Z");

  it("sorts rows into the bucket their timestamp falls into", () => {
    const rows = [
      { created_at: "2026-08-24T01:00:00Z" }, // bucket 0 of 4 (first 6h)
      { created_at: "2026-08-24T19:00:00Z" }, // bucket 3 of 4 (last 6h)
    ];

    const buckets = bucketByTime(rows, 4, windowStart, windowEnd);

    expect(buckets).toHaveLength(4);
    expect(buckets[0]).toEqual([rows[0]]);
    expect(buckets[3]).toEqual([rows[1]]);
    expect(buckets[1]).toEqual([]);
    expect(buckets[2]).toEqual([]);
  });

  it("clamps a row whose timestamp lands exactly on windowEnd into the last bucket rather than dropping it", () => {
    const rows = [{ created_at: windowEnd.toISOString() }];
    const buckets = bucketByTime(rows, 3, windowStart, windowEnd);

    expect(buckets[2]).toEqual(rows);
  });

  it("drops rows with an unparseable timestamp instead of throwing", () => {
    const rows = [{ created_at: "not-a-date" }];
    const buckets = bucketByTime(rows, 2, windowStart, windowEnd);

    expect(buckets[0]).toEqual([]);
    expect(buckets[1]).toEqual([]);
  });

  it("returns all-empty buckets rather than dividing by zero when the window has no span", () => {
    const buckets = bucketByTime(
      [{ created_at: "2026-08-24T01:00:00Z" }],
      3,
      windowStart,
      windowStart,
    );
    expect(buckets).toEqual([[], [], []]);
  });
});

describe("hasWindowSpan / priorPeriodBounds reject Object.prototype-colliding keys", () => {
  // Live-reproduced against a real deployed stack with ?window=toString
  // (docs/proof/analytics-overview-parity-2026-08-28/capture-log.txt): the
  // page reads `window` straight off the URL query string with no
  // allowlist, and a plain `window in spanMap` / `spanMap[window]` walks
  // the prototype chain, resolving these five names to an inherited,
  // always-truthy function instead of undefined -- which used to crash the
  // whole overview tab with an uncaught RangeError from
  // `new Date(NaN).toISOString()`.
  const poisonKeys = [
    "toString",
    "constructor",
    "valueOf",
    "hasOwnProperty",
    "__proto__",
  ];

  it.each(poisonKeys)(
    "hasWindowSpan(%s) is false, not the truthy inherited member",
    (key) => {
      expect(hasWindowSpan(ANALYTICS_WINDOW_SPAN_MS, key)).toBe(false);
    },
  );

  it.each(poisonKeys)(
    "priorPeriodBounds(%s) returns null rather than throwing",
    (key) => {
      expect(() =>
        priorPeriodBounds(ANALYTICS_WINDOW_SPAN_MS, key, new Date()),
      ).not.toThrow();
      expect(priorPeriodBounds(ANALYTICS_WINDOW_SPAN_MS, key, new Date())).toBeNull();
    },
  );

  it("still recognizes every real window value", () => {
    for (const key of Object.keys(ANALYTICS_WINDOW_SPAN_MS)) {
      expect(hasWindowSpan(ANALYTICS_WINDOW_SPAN_MS, key)).toBe(true);
      expect(priorPeriodBounds(ANALYTICS_WINDOW_SPAN_MS, key, new Date())).not.toBeNull();
    }
  });
});

describe("deriveOverviewTiles never throws on a prototype-colliding window value", () => {
  it.each(["toString", "constructor", "valueOf", "hasOwnProperty", "__proto__"])(
    "treats window=%s as an ordinary unsupported window, not a crash",
    (key) => {
      expect(() =>
        deriveOverviewTiles({
          timeWindow: key,
          usage: [usageRow({ request_count: 5 })],
          previousUsage: [],
          cacheSample: null,
          cacheSampleTruncated: false,
          previousCacheSample: null,
          topKeys: { spend: [], keys: [] },
        }),
      ).not.toThrow();

      const result = deriveOverviewTiles({
        timeWindow: key,
        usage: [usageRow({ request_count: 5 })],
        previousUsage: [],
        cacheSample: null,
        cacheSampleTruncated: false,
        previousCacheSample: null,
        topKeys: { spend: [], keys: [] },
      });
      expect(result.requestsDelta).toBeUndefined();
      expect(result.windowUnsupportedNote).toBe(
        "No comparison or trend for this window. Pick 24h, 7d or 30d.",
      );
    },
  );
});

describe("sampleTimeSpan", () => {
  it("returns null for an empty sample", () => {
    expect(sampleTimeSpan([])).toBeNull();
  });

  it("returns null when every row has an unparseable timestamp", () => {
    expect(sampleTimeSpan([{ created_at: "not-a-date" }])).toBeNull();
  });

  it("returns the earliest and latest real timestamp, ignoring unparseable rows", () => {
    const span = sampleTimeSpan([
      { created_at: "2026-08-24T10:00:00Z" },
      { created_at: "garbage" },
      { created_at: "2026-08-24T08:00:00Z" },
      { created_at: "2026-08-24T12:00:00Z" },
    ]);
    expect(span).toEqual({
      start: new Date("2026-08-24T08:00:00Z"),
      end: new Date("2026-08-24T12:00:00Z"),
    });
  });
});

describe("deriveOverviewTiles", () => {
  it("distinguishes a failed prior-period fetch (unavailable) from a measured zero previous period (a rise off zero) on requests, tokens and spend", () => {
    const failed = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [usageRow({ request_count: 42 })],
      previousUsage: null, // fetchPreviousUsage's own fetch failed
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    });
    expect(failed.requestsDelta?.unavailable).toBe(true);
    expect(failed.creditsDelta?.unavailable).toBe(true);

    const realZero = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [usageRow({ request_count: 42 })],
      previousUsage: [], // fetch succeeded, genuinely nothing last period
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    });
    expect(realZero.requestsDelta?.unavailable).not.toBe(true);
    expect(realZero.requestsDelta?.percent).toBeNull();
    expect(realZero.requestsDelta?.fromZero).toBe(true);
    // The blended price is the exception in the same bundle: a prior period
    // with no tokens has no price at all, so the tile reads "No prior data"
    // rather than a rise off a fabricated zero credits per million.
    expect(realZero.blendedDelta?.percent).toBeNull();
    expect(realZero.blendedDelta?.fromZero).toBeFalsy();
    expect(realZero.blendedDelta?.unavailable).toBeFalsy();
  });

  it("distinguishes a failed top-keys fetch from a real empty result", () => {
    const failed = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [],
      previousUsage: [],
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: null,
    });
    expect(failed.topKeysFailed).toBe(true);
    expect(failed.topKeys).toEqual([]);

    const empty = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [],
      previousUsage: [],
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    });
    expect(empty.topKeysFailed).toBe(false);
    expect(empty.topKeys).toEqual([]);
  });

  it("ranks top keys by spend and joins a real key's nickname, never mislabeling one whose spend and key list came back together", () => {
    const result = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [],
      previousUsage: [],
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: {
        spend: [
          spendRow({ group_key: "key-1", total_credits: 50 }),
          spendRow({ group_key: "key-2", total_credits: 200 }),
        ],
        keys: [
          apiKey({ id: "key-1", nickname: "Production" }),
          apiKey({ id: "key-2", nickname: "Staging" }),
        ],
      },
    });
    expect(result.topKeys.map((k) => k.label)).toEqual(["Staging", "Production"]);
  });

  it("labels the unattributed bucket as Unattributed, never as a deleted key (issue #1347)", () => {
    const result = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [],
      previousUsage: [],
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: {
        // The literal, not the constant: a fixture keyed by the same constant
        // the code compares against would pass even if the constant drifted
        // away from what the control-plane actually sends.
        spend: [spendRow({ group_key: "unattributed", total_credits: 10 })],
        keys: [apiKey({ id: "key-1", nickname: "Production" })],
      },
    });
    // The bucket mixes traffic that never carried a key with keys deleted
    // after the fact, so the label and the suffix both have to hold for
    // either. "Deleted key" does not.
    expect(UNATTRIBUTED_GROUP_KEY).toBe("unattributed");
    expect(result.topKeys[0].label).toBe("Unattributed");
    expect(result.topKeys[0].suffix).toBe("no key on record");
  });

  it("labels a spend row 'Deleted key' only when the account's own key list genuinely has no match", () => {
    const result = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [],
      previousUsage: [],
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: {
        spend: [spendRow({ group_key: "gone-key", total_credits: 10 })],
        keys: [], // spend and keys arrived together, atomically -- this is real
      },
    });
    expect(result.topKeys[0].label).toBe("Deleted key");
  });

  it("explains an unsupported window on every branch: fully supported, delta-only, sparkline-only, and neither", () => {
    const base = {
      usage: [],
      previousUsage: [],
      cacheSample: null,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    };
    // 7d: both the analytics-summary and usage-events endpoints recognize it.
    expect(deriveOverviewTiles({ timeWindow: "7d", ...base }).windowUnsupportedNote).toBeUndefined();
    // 1h: usage-events recognizes it (sparkline OK) but the analytics-summary
    // endpoint's window enum does not (no delta).
    expect(deriveOverviewTiles({ timeWindow: "1h", ...base }).windowUnsupportedNote).toBe(
      "No comparison for this window. Pick 24h, 7d, 30d or 90d.",
    );
    // 90d: the reverse -- analytics-summary recognizes it (delta OK) but
    // usage-events does not (no sparkline sample).
    expect(deriveOverviewTiles({ timeWindow: "90d", ...base }).windowUnsupportedNote).toBe(
      "No trend sample for this window. Pick 1h, 24h, 7d or 30d.",
    );
    // A custom range: reachable from the real "Custom" control in
    // TimeWindowPicker, and recognized by neither endpoint.
    expect(
      deriveOverviewTiles({ timeWindow: "custom:2026-01-01:2026-01-05", ...base })
        .windowUnsupportedNote,
    ).toBe("No comparison or trend for this window. Pick 24h, 7d or 30d.");
  });

  it("buckets sparklines over the sample's own real time span, not the nominal window, so a small recent sample doesn't fabricate an all-recent spike", () => {
    const now = new Date("2026-08-24T12:00:00Z");
    // 8 events, 10 minutes apart, spanning the last 70 minutes -- a sliver
    // of a nominal 30-day window. ListEvents (repository.go) orders
    // created_at DESC and pages, so this is exactly the shape a real
    // 100-row-capped sample takes on an active account: the most recent
    // rows only, clustered near `now`, not spread across the full window.
    const events = Array.from({ length: 8 }, (_, i) =>
      event({
        created_at: new Date(now.getTime() - (70 - i * 10) * 60 * 1000).toISOString(),
      }),
    );

    const result = deriveOverviewTiles({
      timeWindow: "30d",
      usage: [],
      previousUsage: [],
      cacheSample: events,
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    });

    // Bucketed over the sample's real ~70-minute span, each of the 8
    // 10-minutes-apart events lands in its own bucket. Bucketed over the
    // nominal 30-day window instead (the bug), all 8 would collapse into
    // the single most-recent bucket -- a manufactured "surge just
    // happened" shape with no relationship to the account's real trend.
    expect(result.requestsSparkline).toEqual([1, 1, 1, 1, 1, 1, 1, 1]);
  });

  it("states the sample size and truncation in a visible sparkline caption", () => {
    const truncated = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [],
      previousUsage: [],
      cacheSample: [event(), event({ id: "e2", created_at: "2026-08-25T12:00:00Z" })],
      cacheSampleTruncated: true,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    });
    expect(truncated.sparklineCaption).toBe("Trend across the last 2 requests");

    const complete = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [],
      previousUsage: [],
      cacheSample: [event(), event({ id: "e2", created_at: "2026-08-25T12:00:00Z" })],
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    });
    expect(complete.sparklineCaption).toBe("Trend across all 2 requests this window");
  });

  it("draws no sparkline at all for a sample that covers one instant, rather than eight measured zeros", () => {
    // sampleTimeSpan returns start === end for a one-row sample, or for one
    // where every row shares a timestamp, and bucketByTime answers a zero span
    // with all-empty buckets. Rendering those would draw a flat line at zero
    // under a headline number that is not zero.
    const result = deriveOverviewTiles({
      timeWindow: "7d",
      usage: [usageRow()],
      previousUsage: [],
      cacheSample: [event(), event({ id: "e2" })],
      cacheSampleTruncated: false,
      previousCacheSample: null,
      topKeys: { spend: [], keys: [] },
    });

    expect(result.requestsSparkline).toBeUndefined();
    expect(result.cacheHitSparkline).toBeUndefined();
    expect(result.sparklineCaption).toBeUndefined();
  });
});

describe("model catalog cache pricing columns", () => {
  it("renders both cache prices when the alias publishes them", () => {
    render(<ModelCatalogTable models={[model()]} />);

    expect(screen.getByText("Cache read / 1M")).toBeDefined();
    expect(screen.getByText("Cache write / 1M")).toBeDefined();
    // 1,200,000 and 15,000,000 credits per million tokens, rendered as the
    // dollars-per-million figure the catalog now publishes. Both are well
    // under a cent and neither may collapse to "$0.00".
    expect(screen.getByText("$0.0012")).toBeDefined();
    expect(screen.getByText("$0.015")).toBeDefined();
  });

  it("renders a dash, never a zero, when a fixed-price alias publishes no cache rate", () => {
    const { container } = render(
      <ModelCatalogTable
        models={[
          model({
            pricing: {
              input_price_credits: 12_000_000,
              output_price_credits: 36_000_000,
              cache_read_price_credits: null,
              cache_write_price_credits: null,
              pricing_mode: "fixed",
            },
          }),
        ]}
      />,
    );

    const cells = Array.from(container.querySelectorAll("td")).map(
      (cell) => cell.textContent,
    );
    expect(cells).toContain("—");
    expect(cells).not.toContain("0");
  });

  it("says Variable for a cache price on an upstream-priced alias", () => {
    render(
      <ModelCatalogTable
        models={[
          model({
            pricing: {
              input_price_credits: null,
              output_price_credits: null,
              cache_read_price_credits: null,
              cache_write_price_credits: null,
              pricing_mode: "upstream_actual",
            },
          }),
        ]}
      />,
    );

    expect(screen.getAllByText("Variable").length).toBe(4);
  });
});

describe("model catalog search, filter and sort", () => {
  const models = [
    model({
      id: "alpha-chat",
      display_name: "Alpha Chat",
      capability_badges: ["chat"],
      pricing: {
        input_price_credits: 9_000_000,
        output_price_credits: 1_000_000,
        cache_read_price_credits: 900_000,
        cache_write_price_credits: null,
        pricing_mode: "fixed",
      },
    }),
    model({
      id: "beta-embed",
      display_name: "Beta Embed",
      capability_badges: ["embeddings"],
      pricing: {
        input_price_credits: 1_000_000,
        output_price_credits: 2_000_000,
        cache_read_price_credits: null,
        cache_write_price_credits: null,
        pricing_mode: "fixed",
      },
    }),
    model({
      id: "gamma-variable",
      display_name: "Gamma Variable",
      capability_badges: ["chat"],
      pricing: {
        input_price_credits: null,
        output_price_credits: null,
        cache_read_price_credits: null,
        cache_write_price_credits: null,
        pricing_mode: "upstream_actual",
      },
    }),
  ];

  function visibleAliases(container: HTMLElement): string[] {
    return Array.from(container.querySelectorAll("tbody code")).map(
      (node) => node.textContent ?? "",
    );
  }

  it("filters rows by a search term over alias, name and summary", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Search models"), {
      target: { value: "beta" },
    });

    expect(visibleAliases(container)).toEqual(["beta-embed"]);
    expect(screen.getByTestId("catalog-result-count").textContent).toBe(
      "1 of 3 models",
    );
  });

  it("filters rows by capability", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Capability"), {
      target: { value: "embeddings" },
    });

    expect(visibleAliases(container)).toEqual(["beta-embed"]);
  });

  it("sorts an unpriced alias last in a cheapest-first sort, never first", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Sort"), {
      target: { value: "input_asc" },
    });

    expect(visibleAliases(container)).toEqual([
      "beta-embed",
      "alpha-chat",
      "gamma-variable",
    ]);
  });

  it("keeps the unpriced alias last in the descending sort too", () => {
    const { container } = render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Sort"), {
      target: { value: "input_desc" },
    });

    expect(visibleAliases(container).at(-1)).toBe("gamma-variable");
  });

  it("distinguishes an empty filter result from an empty catalog", () => {
    render(<ModelCatalogBrowser models={models} />);

    fireEvent.change(screen.getByLabelText("Search models"), {
      target: { value: "nothing-matches-this" },
    });

    expect(screen.getByText("No models match these filters")).toBeDefined();
    expect(screen.queryByText("No models available")).toBeNull();
  });
});

describe("request log cache token columns", () => {
  it("renders cache read and cache write counts when present", () => {
    render(
      <UsageLogsTable
        rows={[
          event({ cache_read_tokens: 12_345, cache_write_tokens: 678 }),
        ]}
        keyNames={{}}
      />,
    );

    // Scoped to the columnheader role: the table's column-controls checklist
    // also carries "Cached in"/"Cache write" as checkbox labels (same text
    // shown twice by design, once as the header and once as the toggle), so
    // a plain getByText is ambiguous once that checklist exists.
    expect(
      screen.getByRole("columnheader", { name: "Cached in" })
    ).toBeDefined();
    expect(
      screen.getByRole("columnheader", { name: "Cache write" })
    ).toBeDefined();
    expect(screen.getByText("12,345")).toBeDefined();
    expect(screen.getByText("678")).toBeDefined();
  });

  it("renders an em-dash, not a zero, when the field is absent", () => {
    // The control-plane omits both fields when zero, so the console cannot
    // tell an unmeasured value from a measured zero.
    const { container } = render(
      <UsageLogsTable rows={[event()]} keyNames={{}} />,
    );

    const cells = Array.from(container.querySelectorAll("tbody td")).map(
      (cell) => cell.textContent,
    );
    expect(cells.filter((text) => text === "—").length).toBeGreaterThanOrEqual(
      2,
    );
    expect(cells).not.toContain("0");
  });
});

describe("usage CSV export", () => {
  async function exportedCsv(
    rows: UsageEventRow[],
    keyNames: Record<string, string> = {},
  ): Promise<string> {
    let captured = "";
    const createObjectURL = vi
      .spyOn(URL, "createObjectURL")
      .mockImplementation((blob: Blob | MediaSource) => {
        void (blob as Blob)
          .text()
          .then((text) => {
            captured = text;
          })
          .catch(() => {});
        return "blob:mock";
      });
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    render(<UsageLogsCsv rows={rows} keyNames={keyNames} />);
    fireEvent.click(screen.getByText("Export CSV"));

    // Blob.text() resolves on a microtask; flush before asserting.
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(createObjectURL).toHaveBeenCalled();
    return captured;
  }

  it("carries both cache token columns in the header", async () => {
    const csv = await exportedCsv([event({ cache_read_tokens: 900 })]);

    const [header] = csv.split("\r\n");
    expect(header.split(",")).toEqual([
      "created_at",
      "request_id",
      "model_alias",
      "status",
      "input_tokens",
      "output_tokens",
      "cache_read_tokens",
      "cache_write_tokens",
      "hive_credit_delta",
      "latency_ms",
      "error_code",
      "api_key",
    ]);
  });

  it("exports an absent cache count as an empty cell rather than a zero", async () => {
    const csv = await exportedCsv([event({ cache_read_tokens: 900 })]);

    const [, row] = csv.split("\r\n");
    const cells = row.split(",");
    expect(cells[6]).toBe("900");
    expect(cells[7]).toBe("");
  });

  it("exports latency as raw milliseconds, and an absent one as an empty cell", async () => {
    // The CSV is the artefact someone attaches to an incident review, so
    // the number the latency column was added for has to survive the
    // export. Absent stays empty for the same reason the table renders an
    // em-dash: an unmeasured request is not a 0ms one.
    const csv = await exportedCsv([
      event({ latency_ms: 1800 }),
      event({ id: "e2" }),
    ]);

    const [, measured, unmeasured] = csv.split("\r\n");
    expect(measured.split(",")[9]).toBe("1800");
    expect(unmeasured.split(",")[9]).toBe("");
  });

  it("neutralises an api key nickname that opens with a formula character", async () => {
    // The api_key column carries the key's nickname, which is free text a
    // workspace member chose. Excel evaluates a cell that starts with "=",
    // and the person who opens the export is not the person who named the
    // key (issue #1401).
    const csv = await exportedCsv([event({ api_key_id: "k-1" })], {
      "k-1": '=HYPERLINK("http://qa.invalid")',
    });

    const [, row] = csv.split("\r\n");
    expect(row.endsWith('"\'=HYPERLINK(""http://qa.invalid"")"')).toBe(true);
  });

  it("keeps a comma-bearing value intact instead of blanking the comma out", async () => {
    const csv = await exportedCsv([event({ api_key_id: "k-1" })], {
      "k-1": "billing, prod",
    });

    const [, row] = csv.split("\r\n");
    expect(row.endsWith('"billing, prod"')).toBe(true);
  });
});
