import Link from "next/link";
import { redirect } from "next/navigation";

import { getAccountProfile, getViewer } from "@/lib/control-plane/client";
import {
  fetchOverviewData,
  type GroupBy,
  type TabName,
} from "@/lib/analytics/overview-fetch";
import { deriveOverviewTiles } from "@/lib/analytics/cache-metrics";
import {
  ANALYTICS_WINDOW_SPAN_MS,
  hasWindowSpan,
} from "@/lib/analytics/windows";
import { AnalyticsControls } from "@/components/analytics/analytics-controls";
import { AnalyticsOverviewSection } from "@/components/analytics/analytics-overview-section";
import { ObservabilityTiles } from "@/components/analytics/observability-tiles";
import { AnalyticsTable } from "@/components/analytics/analytics-table";
import { ChartCard } from "@/components/analytics/chart-card";
import { ErrorChart } from "@/components/analytics/error-chart";
import { SpendChart } from "@/components/analytics/spend-chart";
import { UsageChart } from "@/components/analytics/usage-chart";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { cn } from "@/lib/cn";

interface AnalyticsPageProps {
  searchParams: Promise<{
    tab?: string;
    group_by?: string;
    window?: string;
  }>;
}

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

// `window` is user controlled and reaches two backends that quietly serve
// their own 7d default for anything they do not recognize
// (parseAnalyticsFilter, apps/control-plane/internal/usage/http.go). An
// unrecognized value therefore rendered 7d rows under a heading naming a
// different window, on every panel at once, including the top keys panel,
// which carries no window note of its own. That is reachable from the real
// "Custom" control in TimeWindowPicker, not just a crafted query string.
// Resolve the value once, here, and hand the resolved window to the fetches,
// the tab links and the picker alike, so what the page says and what it
// fetched are the same window.
function resolveWindow(requested: string | undefined): string {
  return requested && hasWindowSpan(ANALYTICS_WINDOW_SPAN_MS, requested)
    ? requested
    : "7d";
}

const TABS: ReadonlyArray<{ id: TabName; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "usage", label: "Usage" },
  { id: "spend", label: "Spend" },
  { id: "errors", label: "Errors" },
];

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
  const timeWindow = resolveWindow(params.window);

  // Independent round trips, so they run together: the profile only feeds
  // the shell's user menu and nothing in the tab body reads it.
  const [profile, bundle] = await Promise.all([
    getAccountProfile().catch((): { owner_name: string } => ({ owner_name: "" })),
    fetchOverviewData({
      activeTab,
      groupBy,
      timeWindow,
      now: new Date(),
    }),
  ]);

  const fetchError = bundle.main === null;
  const usageData = bundle.main?.usage ?? [];
  const spendData = bundle.main?.spend ?? [];
  const errorData = bundle.main?.errors ?? [];

  const tiles = deriveOverviewTiles({
    timeWindow,
    usage: usageData,
    previousUsage: bundle.previousUsage,
    cacheSample: bundle.cacheSample?.events ?? null,
    cacheSampleTruncated: bundle.cacheSample?.truncated ?? false,
    previousCacheSample: bundle.previousCacheSample,
    topKeys: bundle.topKeys,
  });

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
            <AnalyticsOverviewSection tiles={tiles} usageData={usageData} />
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
