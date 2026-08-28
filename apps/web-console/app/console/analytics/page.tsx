import type { ReactNode } from "react";
import Link from "next/link";
import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getAnalyticsErrors,
  getAnalyticsSpend,
  getAnalyticsUsage,
  getApiKeys,
  getUsageEvents,
  getViewer,
} from "@/lib/control-plane/client";
import type {
  ApiKey,
  ErrorSummaryRow,
  SpendSummaryRow,
  UsageEventRow,
  UsageSummaryRow,
} from "@/lib/control-plane/client";
import {
  bucketByTime,
  deriveBlendedCreditsPerMillion,
  deriveCacheHitRate,
  derivePeriodDelta,
} from "@/lib/analytics/cache-metrics";
import type { PeriodDelta } from "@/lib/analytics/cache-metrics";
import { AnalyticsControls } from "@/components/analytics/analytics-controls";
import { ObservabilityTiles } from "@/components/analytics/observability-tiles";
import { AnalyticsTable } from "@/components/analytics/analytics-table";
import { ErrorChart } from "@/components/analytics/error-chart";
import { SpendChart } from "@/components/analytics/spend-chart";
import { Sparkline } from "@/components/analytics/sparkline";
import { UsageChart } from "@/components/analytics/usage-chart";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";
import { cn } from "@/lib/cn";
import {
  formatCredits,
  formatNumber,
  formatPercent,
} from "@/lib/format/credits";
import { formatUsdFromCredits } from "@/lib/format/model-pricing";

interface AnalyticsPageProps {
  searchParams: Promise<{
    tab?: string;
    group_by?: string;
    window?: string;
  }>;
}

type TabName = "overview" | "usage" | "spend" | "errors";
type GroupBy = "model" | "api_key" | "endpoint";

function isValidTab(tab: string | undefined): tab is TabName {
  return (
    tab === "overview" ||
    tab === "usage" ||
    tab === "spend" ||
    tab === "errors"
  );
}

function isValidGroupBy(value: string | undefined): value is GroupBy {
  return value === "model" || value === "api_key" || value === "endpoint";
}

const TABS: ReadonlyArray<{ id: TabName; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "usage", label: "Usage" },
  { id: "spend", label: "Spend" },
  { id: "errors", label: "Errors" },
];

interface SummaryCardProps {
  label: string;
  value: string;
  // Says what the number above actually covers. Required on any tile whose
  // value is derived rather than read straight off the server aggregate, so
  // the sample or the formula is never left implicit.
  note?: string;
  testId?: string;
  // Percent change vs the prior equal-length period, computed from the same
  // full server aggregate as `value` (see derivePeriodDelta). Omitted
  // entirely, not rendered as an em-dash, when the window has no defined
  // prior period (a custom range) — the tile just carries no delta row.
  delta?: PeriodDelta;
  // Per-bucket trend across the bounded usage_events sample the cache tile
  // already fetches (EVENT_SAMPLE_WINDOWS). A sample, not the full window;
  // see the Sparkline component and bucketByTime for the honesty contract.
  sparkline?: ReadonlyArray<number | null>;
}

function formatDeltaPercent(percent: number): string {
  const magnitude = Math.abs(percent);
  const digits = magnitude >= 100 ? 0 : 1;
  return `${magnitude.toFixed(digits)}%`;
}

function DeltaRow({ delta }: { delta: PeriodDelta }) {
  if (delta.percent === null) {
    return (
      <p className="text-2xs leading-tight text-[var(--color-ink-3)]">
        No prior data
      </p>
    );
  }
  const colorClass =
    delta.direction === "up"
      ? "text-[var(--color-success)]"
      : delta.direction === "down"
        ? "text-[var(--color-danger)]"
        : "text-[var(--color-ink-3)]";
  const arrow =
    delta.direction === "up" ? "↑" : delta.direction === "down" ? "↓" : "→";
  return (
    <p className={cn("text-2xs leading-tight", colorClass)}>
      {arrow} {formatDeltaPercent(delta.percent)} vs prior period
    </p>
  );
}

function SummaryCard({
  label,
  value,
  note,
  testId,
  delta,
  sparkline,
}: SummaryCardProps) {
  return (
    <Card>
      <CardContent className="flex items-start justify-between gap-3 px-5 py-5">
        <div className="flex min-w-0 flex-col gap-1">
          <p className="text-2xs font-medium uppercase tracking-wider text-[var(--color-ink-3)]">
            {label}
          </p>
          <p
            className="metric text-2xl text-[var(--color-ink)]"
            data-numeric
            data-testid={testId}
          >
            {value}
          </p>
          {delta ? <DeltaRow delta={delta} /> : null}
          {note ? (
            <p className="text-2xs leading-tight text-[var(--color-ink-3)]">
              {note}
            </p>
          ) : null}
        </div>
        {sparkline ? (
          <div className="shrink-0 pt-1" aria-hidden="true">
            <Sparkline values={sparkline} />
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

// Windows the usage-events endpoint accepts as a preset. The analytics
// controls also offer 90d and a custom range, which parseListEventsFilter
// (apps/control-plane/internal/usage/http.go) rejects with a 400. Rather than
// fire a request that is known to fail, or quietly substitute a different
// window and label the answer as though it covered the one on screen, the
// cache tile says it has no sample for those windows.
//
// ponytail: preset windows only. Widening it means teaching getUsageEvents to
// pass explicit from/to bounds, which the endpoint already accepts.
const EVENT_SAMPLE_WINDOWS: ReadonlyArray<string> = ["1h", "24h", "7d", "30d"];

// The usage-events endpoint caps a page at 100 rows (maxEventsPageLimit), so
// the cache sample is the most recent 100 requests in the window and every
// tile built from it says so.
const CACHE_SAMPLE_LIMIT = 100;

// Millisecond span of each window this endpoint's preset actually recognizes
// (parseAnalyticsFilter in apps/control-plane/internal/usage/http.go; an
// unrecognized value silently falls back to its own 7d default rather than
// 400ing, so this map is deliberately exhaustive and every prior-period
// fetch below checks membership before firing one). Used only to compute the
// explicit from/to bounds of the period immediately BEFORE the one already
// on screen, never to touch the current period's own request.
const ANALYTICS_WINDOW_SPAN_MS: Readonly<Record<string, number>> = {
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
  "90d": 90 * 24 * 60 * 60 * 1000,
};

// Same idea, scoped to the usage-events window enum (EVENT_SAMPLE_WINDOWS
// above), which recognizes "1h" but not "90d" -- a different set from the
// analytics-summary endpoint on purpose, matching the two backend handlers.
const EVENT_WINDOW_SPAN_MS: Readonly<Record<string, number>> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
};

// How many points a tile sparkline draws. Fixed and small: this is a shape
// indicator next to a headline number, not a chart anyone reads axis values
// off of.
const SPARKLINE_BUCKETS = 8;

// Bounds of the equal-length period immediately before the one `window`
// names, as explicit ISO8601 from/to. Null when `window` is not one of the
// preset values `spanMap` recognizes (a custom range, or an unknown string),
// since there is no principled "prior period" for either of those.
function priorPeriodBounds(
  spanMap: Readonly<Record<string, number>>,
  window: string,
  now: Date,
): { from: string; to: string } | null {
  const spanMs = spanMap[window];
  if (!spanMs) {
    return null;
  }
  const to = new Date(now.getTime() - spanMs);
  const from = new Date(to.getTime() - spanMs);
  return { from: from.toISOString(), to: to.toISOString() };
}

// Top API keys panel caps at this many rows, matching the reference's own
// "Top API Keys" panel width.
const TOP_KEYS_LIMIT = 5;

export default async function AnalyticsPage({
  searchParams,
}: AnalyticsPageProps) {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const params = await searchParams;
  const activeTab: TabName = isValidTab(params.tab) ? params.tab : "overview";
  const groupBy: GroupBy = isValidGroupBy(params.group_by)
    ? params.group_by
    : "model";
  const timeWindow = params.window ?? "7d";

  const profile = await getAccountProfile().catch(
    (): { owner_name: string } => ({ owner_name: "" }),
  );

  const fetchParams = { group_by: groupBy, window: timeWindow };
  // One reference instant for every prior-period bound computed below, so a
  // tile's delta and its sparkline can't drift against each other from two
  // different calls to Date.now() mid-render.
  const now = new Date();

  // The tab's primary data. A rejection here is the one failure that shows
  // the page-level error banner instead of degrading a single tile; every
  // other fetch below is independent of this one and of each other, so all
  // of them run concurrently rather than as one long sequential chain of
  // round trips (five sequential control-plane calls on every overview page
  // load was the previous shape of this function, and it showed up as real
  // added latency, not just a slower test).
  async function fetchMain(): Promise<{
    usage: UsageSummaryRow[];
    spend: SpendSummaryRow[];
    errors: ErrorSummaryRow[];
  } | null> {
    try {
      if (activeTab === "overview") {
        const [usage, spend, errors] = await Promise.all([
          getAnalyticsUsage(fetchParams),
          getAnalyticsSpend(fetchParams),
          getAnalyticsErrors(fetchParams),
        ]);
        return { usage, spend, errors };
      }
      if (activeTab === "usage") {
        return { usage: await getAnalyticsUsage(fetchParams), spend: [], errors: [] };
      }
      if (activeTab === "spend") {
        return { usage: [], spend: await getAnalyticsSpend(fetchParams), errors: [] };
      }
      return { usage: [], spend: [], errors: await getAnalyticsErrors(fetchParams) };
    } catch {
      return null;
    }
  }

  // Prior-period totals for the tile deltas. Grouping choice doesn't matter
  // here (only the summed totals are read), so this always asks for "model"
  // regardless of the group_by the user picked for the table below, and a
  // failure here never sets fetchError: a missing prior period degrades to
  // "No prior data" on every tile (derivePeriodDelta's previous<=0 branch),
  // not a broken overview page.
  async function fetchPreviousUsage(): Promise<UsageSummaryRow[]> {
    if (activeTab !== "overview") {
      return [];
    }
    const bounds = priorPeriodBounds(ANALYTICS_WINDOW_SPAN_MS, timeWindow, now);
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
      return [];
    }
  }

  // Cache hit rate has no server-side aggregate to read (GetUsageSummary sums
  // input, output, credits and count only), so it is derived here from real
  // usage_events rows. The sample is bounded by the endpoint's page cap, and
  // the tile prints the sample size rather than implying it covered the whole
  // window.
  async function fetchCacheSample(): Promise<{
    events: UsageEventRow[];
    truncated: boolean;
  } | null> {
    if (activeTab !== "overview" || !EVENT_SAMPLE_WINDOWS.includes(timeWindow)) {
      return null;
    }
    try {
      const page = await getUsageEvents({
        limit: CACHE_SAMPLE_LIMIT,
        window: timeWindow,
      });
      return { events: page.events, truncated: page.next_cursor !== null };
    } catch {
      // Null: the tile renders as unavailable rather than as a zero-percent
      // hit rate nobody measured.
      return null;
    }
  }

  // Same bounded-sample approach, one period back, purely to give the cache
  // tile a delta. Fetched with explicit from/to (never combined with
  // `window`, which the endpoint 400s on) so it lines up with the analytics
  // delta above rather than the endpoint's own relative-to-request-time now.
  async function fetchPreviousCacheSample(): Promise<UsageEventRow[] | null> {
    if (activeTab !== "overview" || !EVENT_SAMPLE_WINDOWS.includes(timeWindow)) {
      return null;
    }
    const bounds = priorPeriodBounds(EVENT_WINDOW_SPAN_MS, timeWindow, now);
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

  // Top spenders by API key, own account's own keys only (never provider
  // identity). A failure here empties the panel rather than breaking the
  // rest of the page.
  async function fetchTopKeys(): Promise<{
    spend: SpendSummaryRow[];
    keys: ApiKey[];
  }> {
    if (activeTab !== "overview") {
      return { spend: [], keys: [] };
    }
    try {
      const [spend, keys] = await Promise.all([
        getAnalyticsSpend({ group_by: "api_key", window: timeWindow }),
        getApiKeys(),
      ]);
      return { spend, keys };
    } catch {
      return { spend: [], keys: [] };
    }
  }

  const [
    mainResult,
    previousUsageData,
    cacheSampleResult,
    previousCacheSample,
    topKeysResult,
  ] = await Promise.all([
    fetchMain(),
    fetchPreviousUsage(),
    fetchCacheSample(),
    fetchPreviousCacheSample(),
    fetchTopKeys(),
  ]);

  const fetchError = mainResult === null;
  const usageData = mainResult?.usage ?? [];
  const spendData = mainResult?.spend ?? [];
  const errorData = mainResult?.errors ?? [];
  const cacheSample = cacheSampleResult?.events ?? null;
  const cacheSampleTruncated = cacheSampleResult?.truncated ?? false;
  const topKeysRows = topKeysResult.spend;
  const apiKeys = topKeysResult.keys;

  const totalRequests = usageData.reduce(
    (sum, r) => sum + r.request_count,
    0,
  );
  const totalInputTokens = usageData.reduce(
    (sum, r) => sum + r.total_input_tokens,
    0,
  );
  const totalOutputTokens = usageData.reduce(
    (sum, r) => sum + r.total_output_tokens,
    0,
  );
  const totalCreditsSpent = usageData.reduce(
    (sum, r) => sum + r.total_credits_spent,
    0,
  );

  // Prior-period totals for the tile deltas. Grouping choice doesn't matter
  // here (only the summed totals are read), so fetchPreviousUsage above
  // always asks for "model" regardless of the group_by the user picked for
  // the table below, and a missing prior period degrades every tile to "No
  // prior data" (derivePeriodDelta's previous<=0 branch) rather than
  // breaking the page.
  const previousTotalRequests = previousUsageData.reduce(
    (sum, r) => sum + r.request_count,
    0,
  );
  const previousTotalTokens = previousUsageData.reduce(
    (sum, r) => sum + r.total_input_tokens + r.total_output_tokens,
    0,
  );
  const previousTotalInputTokens = previousUsageData.reduce(
    (sum, r) => sum + r.total_input_tokens,
    0,
  );
  const previousTotalOutputTokens = previousUsageData.reduce(
    (sum, r) => sum + r.total_output_tokens,
    0,
  );
  const previousTotalCreditsSpent = previousUsageData.reduce(
    (sum, r) => sum + r.total_credits_spent,
    0,
  );

  const apiKeyById = new Map(apiKeys.map((key) => [key.id, key]));
  const topKeys = [...topKeysRows]
    .sort((a, b) => b.total_credits - a.total_credits)
    .slice(0, TOP_KEYS_LIMIT)
    .map((row) => {
      const key = apiKeyById.get(row.group_key);
      return {
        id: row.group_key,
        label: key ? key.nickname : "Deleted key",
        suffix: key?.redacted_suffix ?? row.group_key.slice(0, 8),
        credits: row.total_credits,
      };
    });

  const cacheHitRate = cacheSample ? deriveCacheHitRate(cacheSample) : null;
  const previousCacheHitRate = previousCacheSample
    ? deriveCacheHitRate(previousCacheSample)
    : null;
  const blendedCreditsPerMillion = deriveBlendedCreditsPerMillion(
    totalCreditsSpent,
    totalInputTokens + totalOutputTokens,
  );
  const previousBlendedCreditsPerMillion = deriveBlendedCreditsPerMillion(
    previousTotalCreditsSpent,
    previousTotalTokens,
  );

  const hasDeltaWindow = timeWindow in ANALYTICS_WINDOW_SPAN_MS;
  const requestsDelta = hasDeltaWindow
    ? derivePeriodDelta(totalRequests, previousTotalRequests)
    : undefined;
  const inputTokensDelta = hasDeltaWindow
    ? derivePeriodDelta(totalInputTokens, previousTotalInputTokens)
    : undefined;
  const outputTokensDelta = hasDeltaWindow
    ? derivePeriodDelta(totalOutputTokens, previousTotalOutputTokens)
    : undefined;
  const creditsDelta = hasDeltaWindow
    ? derivePeriodDelta(totalCreditsSpent, previousTotalCreditsSpent)
    : undefined;
  const cacheHitDelta =
    cacheHitRate?.rate != null && previousCacheHitRate?.rate != null
      ? derivePeriodDelta(cacheHitRate.rate, previousCacheHitRate.rate)
      : undefined;
  const blendedDelta =
    blendedCreditsPerMillion !== null &&
    previousBlendedCreditsPerMillion !== null
      ? derivePeriodDelta(
          blendedCreditsPerMillion,
          previousBlendedCreditsPerMillion,
        )
      : undefined;

  // Sparklines: bucket the CURRENT cache sample by time, never the prior
  // one, and never a second network round trip. Every series below reads
  // fields the sample already carries.
  let requestsSparkline: number[] | undefined;
  let inputTokensSparkline: number[] | undefined;
  let outputTokensSparkline: number[] | undefined;
  let creditsSparkline: number[] | undefined;
  let cacheHitSparkline: Array<number | null> | undefined;
  if (cacheSample && cacheSample.length > 0) {
    const spanMs = EVENT_WINDOW_SPAN_MS[timeWindow];
    const windowStart = new Date(now.getTime() - spanMs);
    const buckets = bucketByTime(cacheSample, SPARKLINE_BUCKETS, windowStart, now);
    requestsSparkline = buckets.map((bucket) => bucket.length);
    inputTokensSparkline = buckets.map((bucket) =>
      bucket.reduce((sum, row) => sum + row.input_tokens, 0),
    );
    outputTokensSparkline = buckets.map((bucket) =>
      bucket.reduce((sum, row) => sum + row.output_tokens, 0),
    );
    creditsSparkline = buckets.map((bucket) =>
      bucket.reduce(
        (sum, row) => sum + Math.max(0, -row.hive_credit_delta),
        0,
      ),
    );
    cacheHitSparkline = buckets.map(
      (bucket) => deriveCacheHitRate(bucket).rate,
    );
  }

  function cacheHitNote(): string {
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

  const uncachedTokens =
    cacheHitRate && cacheHitRate.promptTokens > cacheHitRate.cachedTokens
      ? cacheHitRate.promptTokens - cacheHitRate.cachedTokens
      : 0;

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/analytics"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Analytics</span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Usage and analytics"
        description="Inspect requests, tokens, spend and errors broken down by model, key or endpoint."
      />

      <nav
        aria-label="Analytics sections"
        className="mb-6 flex items-center gap-1 border-b border-[var(--color-border)]"
      >
        {TABS.map((tab) => {
          const isActive = activeTab === tab.id;
          return (
            <Link
              key={tab.id}
              href={`/console/analytics?tab=${tab.id}&group_by=${groupBy}&window=${timeWindow}`}
              className={cn(
                "relative -mb-px inline-flex h-9 items-center px-3 text-sm transition-colors",
                isActive
                  ? "border-b-2 border-[var(--color-ink)] text-[var(--color-ink)]"
                  : "border-b-2 border-transparent text-[var(--color-ink-3)] hover:text-[var(--color-ink)]",
              )}
            >
              {tab.label}
            </Link>
          );
        })}
      </nav>

      <AnalyticsControls
        currentGroupBy={groupBy}
        currentWindow={timeWindow}
        activeTab={activeTab}
      />

      <div className="mt-6">
        <ObservabilityTiles />
      </div>

      {fetchError ? (
        <div
          role="alert"
          className="mb-6 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger-soft)] px-4 py-3 text-sm text-[var(--color-danger)]"
        >
          Unable to load analytics. Refresh to try again.
        </div>
      ) : (
        <>
          {activeTab === "overview" ? (
            <div className="flex flex-col gap-6">
              <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                <SummaryCard
                  label="Total requests"
                  value={formatCredits(totalRequests)}
                  delta={requestsDelta}
                  sparkline={requestsSparkline}
                />
                <SummaryCard
                  label="Input tokens"
                  value={formatCredits(totalInputTokens)}
                  delta={inputTokensDelta}
                  sparkline={inputTokensSparkline}
                />
                <SummaryCard
                  label="Output tokens"
                  value={formatCredits(totalOutputTokens)}
                  delta={outputTokensDelta}
                  sparkline={outputTokensSparkline}
                />
                <SummaryCard
                  label="Total spend"
                  value={formatUsdFromCredits(totalCreditsSpent)}
                  delta={creditsDelta}
                  sparkline={creditsSparkline}
                  testId="total-spend"
                />
                <SummaryCard
                  label="Cache hit rate"
                  value={formatPercent(cacheHitRate?.rate ?? null)}
                  note={cacheHitNote()}
                  delta={cacheHitDelta}
                  sparkline={cacheHitSparkline}
                  testId="cache-hit-rate"
                />
                <SummaryCard
                  label="Blended price / 1M"
                  value={
                    blendedCreditsPerMillion === null
                      ? "—"
                      : formatUsdFromCredits(blendedCreditsPerMillion)
                  }
                  note={
                    blendedCreditsPerMillion === null
                      ? "No tokens served in this window."
                      : `${formatNumber(blendedCreditsPerMillion)} credits. Credits spent divided by input plus output tokens, per million. Effective, so cache reads are already priced in.`
                  }
                  delta={blendedDelta}
                  testId="blended-credits-per-million"
                />
              </section>
              <ChartCard title="Usage" description="Requests and tokens.">
                <UsageChart data={usageData} />
              </ChartCard>
              <div className="grid gap-6 lg:grid-cols-2">
                <Card>
                  <CardHeader>
                    <CardTitle>Cached vs uncached</CardTitle>
                    <CardDescription>
                      Prompt tokens served from cache versus tokens re-processed
                      fresh, over the same bounded sample as the cache hit rate
                      tile above.
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="px-5 py-5">
                    {cacheHitRate &&
                    cacheHitRate.sampleSize > 0 &&
                    cacheHitRate.promptTokens > 0 ? (
                      <div className="flex flex-col gap-4">
                        <CacheSplitBar
                          cachedTokens={cacheHitRate.cachedTokens}
                          uncachedTokens={uncachedTokens}
                        />
                        <dl className="grid grid-cols-2 gap-3 text-sm">
                          <div>
                            <dt className="text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
                              Cached
                            </dt>
                            <dd className="metric text-lg text-[var(--color-ink)]">
                              {formatCredits(cacheHitRate.cachedTokens)}
                            </dd>
                          </div>
                          <div>
                            <dt className="text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
                              Uncached
                            </dt>
                            <dd className="metric text-lg text-[var(--color-ink)]">
                              {formatCredits(uncachedTokens)}
                            </dd>
                          </div>
                        </dl>
                      </div>
                    ) : (
                      <p className="text-sm text-[var(--color-ink-3)]">
                        {cacheHitNote()}
                      </p>
                    )}
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader>
                    <CardTitle>Top API keys by spend</CardTitle>
                    <CardDescription>
                      Your own account&apos;s keys, ranked by credits charged
                      this window.
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="px-5 py-5">
                    {topKeys.length > 0 ? (
                      <ol className="flex flex-col gap-3">
                        {topKeys.map((row, index) => (
                          <li
                            key={row.id}
                            className="flex items-center justify-between gap-3 text-sm"
                          >
                            <span className="flex min-w-0 items-center gap-2 text-[var(--color-ink)]">
                              <span className="text-2xs text-[var(--color-ink-3)]">
                                {index + 1}
                              </span>
                              <span className="truncate">{row.label}</span>
                              <span className="shrink-0 text-2xs text-[var(--color-ink-3)]">
                                {row.suffix}
                              </span>
                            </span>
                            <span className="metric shrink-0 text-[var(--color-ink)]">
                              {formatUsdFromCredits(row.credits)}
                            </span>
                          </li>
                        ))}
                      </ol>
                    ) : (
                      <p className="text-sm text-[var(--color-ink-3)]">
                        No API keys with spend in this window.
                      </p>
                    )}
                  </CardContent>
                </Card>
              </div>
            </div>
          ) : null}

          {activeTab === "usage" ? (
            <div className="flex flex-col gap-6">
              <ChartCard title="Usage" description="Requests and tokens.">
                <UsageChart data={usageData} />
              </ChartCard>
              <AnalyticsTable
                data={usageData.map((r) => ({
                  group_key: r.group_key,
                  total_input_tokens: r.total_input_tokens,
                  total_output_tokens: r.total_output_tokens,
                  total_credits_spent: r.total_credits_spent,
                  request_count: r.request_count,
                }))}
                columns={[
                  { key: "group_key", label: "Group" },
                  { key: "total_input_tokens", label: "Input tokens" },
                  { key: "total_output_tokens", label: "Output tokens" },
                  { key: "total_credits_spent", label: "Credits" },
                  { key: "request_count", label: "Requests" },
                ]}
              />
            </div>
          ) : null}

          {activeTab === "spend" ? (
            <div className="flex flex-col gap-6">
              <ChartCard
                title="Spend"
                description="Credits charged and ledger entries."
              >
                <SpendChart data={spendData} />
              </ChartCard>
              <AnalyticsTable
                data={spendData.map((r) => ({
                  group_key: r.group_key,
                  total_credits: r.total_credits,
                  entry_count: r.entry_count,
                }))}
                columns={[
                  { key: "group_key", label: "Group" },
                  { key: "total_credits", label: "Credits" },
                  { key: "entry_count", label: "Transactions" },
                ]}
              />
            </div>
          ) : null}

          {activeTab === "errors" ? (
            <div className="flex flex-col gap-6">
              <ChartCard
                title="Errors"
                description="Error count and rate by group."
              >
                <ErrorChart data={errorData} />
              </ChartCard>
              <AnalyticsTable
                data={errorData.map((r) => ({
                  group_key: r.group_key,
                  error_count: r.error_count,
                  total_requests: r.total_requests,
                  error_rate: `${(r.error_rate * 100).toFixed(1)}%`,
                }))}
                columns={[
                  { key: "group_key", label: "Group" },
                  { key: "error_count", label: "Errors" },
                  { key: "total_requests", label: "Requests" },
                  { key: "error_rate", label: "Error rate" },
                ]}
              />
            </div>
          ) : null}
        </>
      )}
    </ConsoleShell>
  );
}

interface ChartCardProps {
  title: string;
  description?: string;
  children: ReactNode;
}

function ChartCard({ title, description, children }: ChartCardProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent className="px-5 py-5">{children}</CardContent>
    </Card>
  );
}

interface CacheSplitBarProps {
  cachedTokens: number;
  uncachedTokens: number;
}

// Single two-segment bar, cached share first. Renders an all-uncached bar
// rather than an empty one when cachedTokens is a real, measured zero (a
// window with prompt tokens but no cache hits at all) -- distinct from the
// "no sample" case the caller already routes to cacheHitNote() instead of
// rendering this component at all.
function CacheSplitBar({ cachedTokens, uncachedTokens }: CacheSplitBarProps) {
  const total = cachedTokens + uncachedTokens;
  const cachedPercent = total > 0 ? (cachedTokens / total) * 100 : 0;
  return (
    <div
      className="flex h-3 w-full overflow-hidden rounded-full bg-[var(--color-surface-2)]"
      role="img"
      aria-label={`${formatPercent(total > 0 ? cachedTokens / total : null)} of prompt tokens served from cache`}
    >
      <div
        className="h-full bg-[var(--color-success)]"
        style={{ width: `${cachedPercent}%` }}
      />
    </div>
  );
}
