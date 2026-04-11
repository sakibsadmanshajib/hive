import {
  getAnalyticsUsage,
  getAnalyticsSpend,
  getAnalyticsErrors,
} from "@/lib/control-plane/client";
import type { UsageSummaryRow, SpendSummaryRow, ErrorSummaryRow } from "@/lib/control-plane/client";
import { UsageChart } from "@/components/analytics/usage-chart";
import { SpendChart } from "@/components/analytics/spend-chart";
import { ErrorChart } from "@/components/analytics/error-chart";
import { AnalyticsTable } from "@/components/analytics/analytics-table";
import { AnalyticsControls } from "@/components/analytics/analytics-controls";

interface AnalyticsPageProps {
  searchParams: Promise<{
    tab?: string;
    group_by?: string;
    window?: string;
  }>;
}

type TabName = "overview" | "usage" | "spend" | "errors";

function isValidTab(tab: string | undefined): tab is TabName {
  return tab === "overview" || tab === "usage" || tab === "spend" || tab === "errors";
}

function isValidGroupBy(value: string | undefined): value is "model" | "api_key" | "endpoint" {
  return value === "model" || value === "api_key" || value === "endpoint";
}

const TABS: { id: TabName; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "usage", label: "Usage" },
  { id: "spend", label: "Spend" },
  { id: "errors", label: "Errors" },
];

export default async function AnalyticsPage({ searchParams }: AnalyticsPageProps) {
  const params = await searchParams;
  const activeTab: TabName = isValidTab(params.tab) ? params.tab : "overview";
  const groupBy = isValidGroupBy(params.group_by) ? params.group_by : "model";
  const timeWindow = params.window ?? "7d";

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

  const totalRequests = usageData.reduce((sum, r) => sum + r.request_count, 0);
  const totalInputTokens = usageData.reduce((sum, r) => sum + r.total_input_tokens, 0);
  const totalOutputTokens = usageData.reduce((sum, r) => sum + r.total_output_tokens, 0);
  const totalCreditsSpent = usageData.reduce((sum, r) => sum + r.total_credits_spent, 0);

  return (
    <div style={{ maxWidth: "72rem" }}>
      <h1 style={{ margin: "0 0 1.5rem", fontSize: "1.5rem", fontWeight: 700 }}>
        Usage &amp; Analytics
      </h1>

      {/* Tab bar */}
      <div
        style={{
          display: "flex",
          borderBottom: "1px solid #e5e7eb",
          gap: "0",
          marginBottom: "1.5rem",
        }}
      >
        {TABS.map((tab) => {
          const isActive = activeTab === tab.id;
          return (
            <a
              key={tab.id}
              href={`/console/analytics?tab=${tab.id}&group_by=${groupBy}&window=${timeWindow}`}
              style={{
                padding: "0.5rem 1rem",
                color: isActive ? "#1d4ed8" : "#6b7280",
                borderBottom: isActive ? "2px solid #1d4ed8" : "2px solid transparent",
                fontWeight: isActive ? 700 : 400,
                textDecoration: "none",
                fontSize: "1rem",
              }}
            >
              {tab.label}
            </a>
          );
        })}
      </div>

      {/* Controls */}
      <AnalyticsControls
        currentGroupBy={groupBy}
        currentWindow={timeWindow}
        activeTab={activeTab}
      />

      {fetchError && (
        <div
          style={{
            padding: "1rem",
            backgroundColor: "#fef2f2",
            border: "1px solid #fecaca",
            borderRadius: "0.5rem",
            color: "#dc2626",
            marginBottom: "1.5rem",
          }}
        >
          Unable to load analytics. Refresh to try again.
        </div>
      )}

      {!fetchError && (
        <>
          {activeTab === "overview" && (
            <OverviewTab
              usageData={usageData}
              totalRequests={totalRequests}
              totalInputTokens={totalInputTokens}
              totalOutputTokens={totalOutputTokens}
              totalCreditsSpent={totalCreditsSpent}
            />
          )}

          {activeTab === "usage" && (
            <UsageTab usageData={usageData} />
          )}

          {activeTab === "spend" && (
            <SpendTab spendData={spendData} />
          )}

          {activeTab === "errors" && (
            <ErrorsTab errorData={errorData} />
          )}
        </>
      )}
    </div>
  );
}

function SummaryCard({
  label,
  value,
}: {
  label: string;
  value: string;
}) {
  return (
    <div
      style={{
        backgroundColor: "#f9fafb",
        border: "1px solid #e5e7eb",
        borderRadius: "0.75rem",
        padding: "1rem 1.25rem",
      }}
    >
      <p style={{ margin: "0 0 0.25rem", fontSize: "0.75rem", color: "#6b7280", fontWeight: 500 }}>
        {label}
      </p>
      <p style={{ margin: 0, fontSize: "1.5rem", fontWeight: 700, color: "#111827" }}>{value}</p>
    </div>
  );
}

interface OverviewTabProps {
  usageData: UsageSummaryRow[];
  totalRequests: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  totalCreditsSpent: number;
}

function OverviewTab({
  usageData,
  totalRequests,
  totalInputTokens,
  totalOutputTokens,
  totalCreditsSpent,
}: OverviewTabProps) {
  return (
    <div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))",
          gap: "1rem",
          marginBottom: "1.5rem",
        }}
      >
        <SummaryCard label="Total requests" value={totalRequests.toLocaleString()} />
        <SummaryCard label="Input tokens" value={totalInputTokens.toLocaleString()} />
        <SummaryCard label="Output tokens" value={totalOutputTokens.toLocaleString()} />
        <SummaryCard label="Credits spent" value={totalCreditsSpent.toLocaleString()} />
      </div>
      <UsageChart data={usageData} />
    </div>
  );
}

function UsageTab({ usageData }: { usageData: UsageSummaryRow[] }) {
  const tableData = usageData.map((r) => ({
    group_key: r.group_key,
    total_input_tokens: r.total_input_tokens,
    total_output_tokens: r.total_output_tokens,
    total_credits_spent: r.total_credits_spent,
    request_count: r.request_count,
  }));

  return (
    <div>
      <UsageChart data={usageData} />
      <AnalyticsTable
        data={tableData}
        columns={[
          { key: "group_key", label: "Group" },
          { key: "total_input_tokens", label: "Input Tokens" },
          { key: "total_output_tokens", label: "Output Tokens" },
          { key: "total_credits_spent", label: "Credits Spent" },
          { key: "request_count", label: "Requests" },
        ]}
      />
    </div>
  );
}

function SpendTab({ spendData }: { spendData: SpendSummaryRow[] }) {
  const tableData = spendData.map((r) => ({
    group_key: r.group_key,
    total_credits: r.total_credits,
    entry_count: r.entry_count,
  }));

  return (
    <div>
      <SpendChart data={spendData} />
      <AnalyticsTable
        data={tableData}
        columns={[
          { key: "group_key", label: "Group" },
          { key: "total_credits", label: "Credits" },
          { key: "entry_count", label: "Transactions" },
        ]}
      />
    </div>
  );
}

function ErrorsTab({ errorData }: { errorData: ErrorSummaryRow[] }) {
  const tableData = errorData.map((r) => ({
    group_key: r.group_key,
    error_count: r.error_count,
    total_requests: r.total_requests,
    error_rate: `${(r.error_rate * 100).toFixed(1)}%`,
  }));

  return (
    <div>
      <ErrorChart data={errorData} />
      <AnalyticsTable
        data={tableData}
        columns={[
          { key: "group_key", label: "Group" },
          { key: "error_count", label: "Errors" },
          { key: "total_requests", label: "Total Requests" },
          { key: "error_rate", label: "Error Rate" },
        ]}
      />
    </div>
  );
}
