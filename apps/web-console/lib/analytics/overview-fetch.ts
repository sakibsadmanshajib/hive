/**
 * All the control-plane I/O behind the analytics overview tab, run
 * concurrently rather than as a sequential chain (five sequential round
 * trips on every overview page load was the previous shape, and it added
 * real latency in production, not just a slower test).
 *
 * Every fetch here that CAN fail independently of the tab's primary data
 * returns null on a real failure, distinct from returning `[]`/an empty
 * result on success. That distinction is load-bearing: `cache-metrics.ts`'s
 * derivations render "the prior period had zero requests" and "we could
 * not ask" as different text, and collapsing the two here (the earlier
 * shape of this file) silently mislabeled an outage as a real zero on the
 * Total requests, Input tokens, Output tokens, Total spend and Top API keys
 * tiles.
 */
import {
  getAnalyticsErrors,
  getAnalyticsSpend,
  getAnalyticsUsage,
  getApiKeys,
  getUsageEvents,
} from "@/lib/control-plane/client";
import type {
  ApiKey,
  ErrorSummaryRow,
  SpendSummaryRow,
  UsageEventRow,
  UsageSummaryRow,
} from "@/lib/control-plane/client";
import {
  ANALYTICS_WINDOW_SPAN_MS,
  CACHE_SAMPLE_LIMIT,
  EVENT_SAMPLE_WINDOWS,
  EVENT_WINDOW_SPAN_MS,
  priorPeriodBounds,
} from "./windows";

export type TabName = "overview" | "usage" | "spend" | "errors";
export type GroupBy = "model" | "api_key" | "endpoint";

export interface OverviewFetchInput {
  activeTab: TabName;
  groupBy: GroupBy;
  timeWindow: string;
  now: Date;
}

export interface MainFetchResult {
  usage: UsageSummaryRow[];
  spend: SpendSummaryRow[];
  errors: ErrorSummaryRow[];
}

export interface CacheSampleResult {
  events: UsageEventRow[];
  truncated: boolean;
}

export interface TopKeysResult {
  spend: SpendSummaryRow[];
  keys: ApiKey[];
}

/**
 * The tab's primary data. A rejection here is the one failure that shows
 * the page-level error banner instead of degrading a single tile.
 */
async function fetchMain(
  input: OverviewFetchInput,
): Promise<MainFetchResult | null> {
  const fetchParams = { group_by: input.groupBy, window: input.timeWindow };
  try {
    if (input.activeTab === "overview") {
      const [usage, spend, errors] = await Promise.all([
        getAnalyticsUsage(fetchParams),
        getAnalyticsSpend(fetchParams),
        getAnalyticsErrors(fetchParams),
      ]);
      return { usage, spend, errors };
    }
    if (input.activeTab === "usage") {
      return { usage: await getAnalyticsUsage(fetchParams), spend: [], errors: [] };
    }
    if (input.activeTab === "spend") {
      return { usage: [], spend: await getAnalyticsSpend(fetchParams), errors: [] };
    }
    return { usage: [], spend: [], errors: await getAnalyticsErrors(fetchParams) };
  } catch {
    return null;
  }
}

/**
 * Prior-period totals for the tile deltas. Grouping choice doesn't matter
 * here (only the summed totals are read), so this always asks for "model"
 * regardless of the group_by the user picked for the table.
 *
 * Returns null ONLY on a real fetch failure, never on "nothing to compare
 * against" (no bounds for this window, or a genuinely empty prior period,
 * which is a real `[]` from the server).
 */
async function fetchPreviousUsage(
  input: OverviewFetchInput,
): Promise<UsageSummaryRow[] | null> {
  if (input.activeTab !== "overview") {
    return [];
  }
  const bounds = priorPeriodBounds(
    ANALYTICS_WINDOW_SPAN_MS,
    input.timeWindow,
    input.now,
  );
  if (!bounds) {
    return [];
  }
  try {
    return await getAnalyticsUsage({
      group_by: "model",
      from: bounds.from,
      to: bounds.to,
    });
  } catch {
    return null;
  }
}

/**
 * Cache hit rate has no server-side aggregate to read (GetUsageSummary sums
 * input, output, credits and count only), so it is derived from real
 * usage_events rows. The sample is bounded by the endpoint's page cap.
 */
async function fetchCacheSample(
  input: OverviewFetchInput,
): Promise<CacheSampleResult | null> {
  if (
    input.activeTab !== "overview" ||
    !EVENT_SAMPLE_WINDOWS.includes(input.timeWindow)
  ) {
    return null;
  }
  try {
    const page = await getUsageEvents({
      limit: CACHE_SAMPLE_LIMIT,
      window: input.timeWindow,
    });
    return { events: page.events, truncated: page.next_cursor !== null };
  } catch {
    return null;
  }
}

/**
 * Same bounded-sample approach, one period back, purely to give the cache
 * tile a delta. Fetched with explicit from/to (never combined with
 * `window`, which the endpoint 400s on).
 */
async function fetchPreviousCacheSample(
  input: OverviewFetchInput,
): Promise<UsageEventRow[] | null> {
  if (
    input.activeTab !== "overview" ||
    !EVENT_SAMPLE_WINDOWS.includes(input.timeWindow)
  ) {
    return null;
  }
  const bounds = priorPeriodBounds(
    EVENT_WINDOW_SPAN_MS,
    input.timeWindow,
    input.now,
  );
  if (!bounds) {
    return null;
  }
  try {
    const page = await getUsageEvents({
      limit: CACHE_SAMPLE_LIMIT,
      from: bounds.from,
      to: bounds.to,
    });
    return page.events;
  } catch {
    return null;
  }
}

/**
 * Top spenders by API key, own account's own keys only (never provider
 * identity). Spend and keys are fetched as one atomic unit (a single
 * Promise.all): either both come back and get used together, or neither
 * does. There is no code path where a real spend row renders against a
 * stale or partial key list, which is what would be needed to mislabel a
 * live key "Deleted key" -- ListKeys never filters by status or paginates
 * (apps/control-plane/internal/apikeys/repository.go), so the only way a
 * spend row key id has no match is the unattributed bucket (issue #1347,
 * matched on UNATTRIBUTED_GROUP_KEY before the key lookup and labelled
 * separately), a genuinely gone key, or a failed
 * fetch, and this function already tells those apart via null.
 */
async function fetchTopKeys(
  input: OverviewFetchInput,
): Promise<TopKeysResult | null> {
  if (input.activeTab !== "overview") {
    return { spend: [], keys: [] };
  }
  try {
    const [spend, keys] = await Promise.all([
      getAnalyticsSpend({ group_by: "api_key", window: input.timeWindow }),
      getApiKeys(),
    ]);
    return { spend, keys };
  } catch {
    return null;
  }
}

export interface OverviewFetchBundle {
  main: MainFetchResult | null;
  previousUsage: UsageSummaryRow[] | null;
  cacheSample: CacheSampleResult | null;
  previousCacheSample: UsageEventRow[] | null;
  topKeys: TopKeysResult | null;
}

/** Runs every fetch above concurrently and returns the raw bundle. */
export async function fetchOverviewData(
  input: OverviewFetchInput,
): Promise<OverviewFetchBundle> {
  const [main, previousUsage, cacheSample, previousCacheSample, topKeys] =
    await Promise.all([
      fetchMain(input),
      fetchPreviousUsage(input),
      fetchCacheSample(input),
      fetchPreviousCacheSample(input),
      fetchTopKeys(input),
    ]);
  return { main, previousUsage, cacheSample, previousCacheSample, topKeys };
}
