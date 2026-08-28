import type { ReactNode } from "react";
import Link from "next/link";
import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getAnalyticsErrors,
  getAnalyticsSpend,
  getAnalyticsUsage,
  getUsageEvents,
  getViewer,
} from "@/lib/control-plane/client";
import type {
  ErrorSummaryRow,
  SpendSummaryRow,
  UsageEventRow,
  UsageSummaryRow,
} from "@/lib/control-plane/client";
import {
  deriveBlendedCreditsPerMillion,
  deriveCacheHitRate,
} from "@/lib/analytics/cache-metrics";
import { AnalyticsControls } from "@/components/analytics/analytics-controls";
import { ObservabilityTiles } from "@/components/analytics/observability-tiles";
import { AnalyticsTable } from "@/components/analytics/analytics-table";
import { ErrorChart } from "@/components/analytics/error-chart";
import { SpendChart } from "@/components/analytics/spend-chart";
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
}

function SummaryCard({ label, value, note, testId }: SummaryCardProps) {
  return (
    <Card>
      <CardContent className="flex flex-col gap-1 px-5 py-5">
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
        {note ? (
          <p className="text-2xs leading-tight text-[var(--color-ink-3)]">
            {note}
          </p>
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

  let usageData: UsageSummaryRow[] = [];
  let spendData: SpendSummaryRow[] = [];
  let errorData: ErrorSummaryRow[] = [];
  let fetchError = false;

  try {
    if (activeTab === "overview") {
      [usageData, spendData, errorData] = await Promise.all([
        getAnalyticsUsage(fetchParams),
        getAnalyticsSpend(fetchParams),
        getAnalyticsErrors(fetchParams),
      ]);
    } else if (activeTab === "usage") {
      usageData = await getAnalyticsUsage(fetchParams);
    } else if (activeTab === "spend") {
      spendData = await getAnalyticsSpend(fetchParams);
    } else if (activeTab === "errors") {
      errorData = await getAnalyticsErrors(fetchParams);
    }
  } catch {
    fetchError = true;
  }

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

  // Cache hit rate has no server-side aggregate to read (GetUsageSummary sums
  // input, output, credits and count only), so it is derived here from real
  // usage_events rows. The sample is bounded by the endpoint's page cap, and
  // the tile prints the sample size rather than implying it covered the whole
  // window.
  let cacheSample: UsageEventRow[] | null = null;
  let cacheSampleTruncated = false;
  if (activeTab === "overview" && EVENT_SAMPLE_WINDOWS.includes(timeWindow)) {
    try {
      const page = await getUsageEvents({
        limit: CACHE_SAMPLE_LIMIT,
        window: timeWindow,
      });
      cacheSample = page.events;
      cacheSampleTruncated = page.next_cursor !== null;
    } catch {
      // Leave cacheSample null: the tile renders as unavailable rather than
      // as a zero-percent hit rate nobody measured.
      cacheSample = null;
    }
  }

  const cacheHitRate = cacheSample ? deriveCacheHitRate(cacheSample) : null;
  const blendedCreditsPerMillion = deriveBlendedCreditsPerMillion(
    totalCreditsSpent,
    totalInputTokens + totalOutputTokens,
  );

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

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
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
                />
                <SummaryCard
                  label="Input tokens"
                  value={formatCredits(totalInputTokens)}
                />
                <SummaryCard
                  label="Output tokens"
                  value={formatCredits(totalOutputTokens)}
                />
                <SummaryCard
                  label="Credits spent"
                  value={formatCredits(totalCreditsSpent)}
                />
                <SummaryCard
                  label="Cache hit rate"
                  value={formatPercent(cacheHitRate?.rate ?? null)}
                  note={cacheHitNote()}
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
                  testId="blended-credits-per-million"
                />
              </section>
              <ChartCard title="Usage" description="Requests and tokens.">
                <UsageChart data={usageData} />
              </ChartCard>
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
