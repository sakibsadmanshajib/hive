/**
 * Issue #1403: /console/analytics?tab=spend&group_by=api_key rendered the
 * GROUP column and the chart axis as raw key UUIDs, while the Overview tab's
 * own "Top API keys by spend" tile, reading the same rows, rendered
 * nicknames. A spend breakdown identified only by UUID cannot answer the
 * question the tab exists to answer.
 *
 * These render the real page Server Component against a mocked control
 * plane, dumping the props the table and the chart are actually handed, the
 * same technique analytics-billing-page-wiring.test.tsx already uses.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const mockGetSession = vi.fn();
const mockGetUser = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: vi.fn(() => ({
    auth: { getUser: mockGetUser, getSession: mockGetSession },
  })),
}));

vi.mock("@/components/app-shell/console-shell", () => ({
  ConsoleShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="console-shell">{children}</div>
  ),
}));

vi.mock("@/components/analytics/analytics-controls", () => ({
  AnalyticsControls: () => <div data-testid="analytics-controls" />,
}));

vi.mock("@/components/analytics/analytics-table", () => ({
  AnalyticsTable: (props: { data: ReadonlyArray<Record<string, unknown>> }) => (
    <div data-testid="analytics-table">{JSON.stringify(props.data)}</div>
  ),
}));

vi.mock("@/components/analytics/spend-chart", () => ({
  SpendChart: (props: { data: ReadonlyArray<Record<string, unknown>> }) => (
    <div data-testid="spend-chart">{JSON.stringify(props.data)}</div>
  ),
}));
vi.mock("@/components/analytics/usage-chart", () => ({
  UsageChart: () => <div data-testid="usage-chart" />,
}));
vi.mock("@/components/analytics/error-chart", () => ({
  ErrorChart: () => <div data-testid="error-chart" />,
}));

const KEY_ID = "890883f4-8da5-474f-8f33-e803f2153c8a";

const VIEWER_PAYLOAD = {
  user: { id: "u1", email: "qa@example.test", email_verified: true },
  current_account: {
    id: "a1",
    display_name: "QA Workspace",
    account_type: "personal",
    role: "owner",
    slug: "qa-workspace",
  },
  memberships: [
    { account_id: "a1", display_name: "QA Workspace", role: "owner", status: "active" },
  ],
  permissions: ["workspace.settings"],
};

const KEY_PAYLOAD = {
  id: KEY_ID,
  nickname: "orchestrator-livecheck",
  status: "active",
  redacted_suffix: "0fae3a",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
  expires_at: null,
  last_used_at: null,
  expiration_summary: { kind: "never", label: "Never expires" },
  budget_summary: { kind: "none", label: "No budget cap" },
  allowlist_summary: { mode: "all", group_names: [], label: "All models" },
  spend_credits: 0,
  budget_limit_credits: null,
  budget_spend_credits: null,
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
}

function stubFetch(apiKeysStatus: number) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
      if (url.endsWith("/api/v1/accounts/current/profile")) {
        return jsonResponse(200, { owner_name: "Ada Owner" });
      }
      if (url.includes("/analytics/spend")) {
        return jsonResponse(200, {
          spend: [
            { group_key: KEY_ID, total_credits: 661_500, entry_count: 1 },
            { group_key: "unattributed", total_credits: 2_442_294, entry_count: 37 },
          ],
        });
      }
      if (url.includes("/analytics/usage")) return jsonResponse(200, { usage: [] });
      if (url.includes("/analytics/errors")) return jsonResponse(200, { errors: [] });
      if (url.includes("/usage-events")) {
        return jsonResponse(200, { events: [], next_cursor: null });
      }
      if (url.includes("/api-keys")) {
        return apiKeysStatus === 200
          ? jsonResponse(200, { items: [KEY_PAYLOAD] })
          : jsonResponse(apiKeysStatus, { error: "nope" });
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
}

async function renderSpendByKey() {
  const mod = await import("../app/console/analytics/page");
  const ui = await mod.default({
    searchParams: Promise.resolve({
      tab: "spend",
      group_by: "api_key",
      window: "30d",
    }),
  });
  render(ui);
}

beforeEach(() => {
  vi.clearAllMocks();
  process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
  mockGetUser.mockResolvedValue({
    data: { user: { id: "u1", email: "qa@example.test" } },
    error: null,
  });
  mockGetSession.mockResolvedValue({
    data: { session: { access_token: "test-token" } },
  });
});

describe("spend grouped by api key", () => {
  it("names the key in the table instead of printing its UUID", async () => {
    stubFetch(200);
    await renderSpendByKey();

    const table = screen.getByTestId("analytics-table").textContent ?? "";
    expect(table).toContain("orchestrator-livecheck");
    expect(table).toContain("0fae3a");
    expect(table).not.toContain(KEY_ID);
  });

  it("names the key on the chart axis too", async () => {
    stubFetch(200);
    await renderSpendByKey();

    const chart = screen.getByTestId("spend-chart").textContent ?? "";
    expect(chart).toContain("orchestrator-livecheck");
    expect(chart).not.toContain(KEY_ID);
  });

  it("still labels the unattributed bucket rather than calling it a key", async () => {
    stubFetch(200);
    await renderSpendByKey();

    expect(screen.getByTestId("analytics-table").textContent).toContain(
      "Unattributed",
    );
  });

  it("leaves the raw id in place when the key list could not be read", async () => {
    // Naming every row "Deleted key" because the lookup failed would be a
    // fabricated answer. An unresolved id is honest; a wrong name is not.
    stubFetch(500);
    await renderSpendByKey();

    const table = screen.getByTestId("analytics-table").textContent ?? "";
    expect(table).toContain(KEY_ID);
    expect(table).not.toContain("Deleted key");
  });
});
