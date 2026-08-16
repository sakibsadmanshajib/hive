/**
 * Issue #856: neither of the two prior test layers would catch a
 * page-wiring regression of exactly this shape. The client-level tests in
 * analytics-client.test.ts pin getAnalyticsUsage/Spend/Errors parsing but
 * never render app/console/analytics/page.tsx, and nothing exercised
 * app/console/billing/page.tsx at all -- which is why that page's hardcoded
 * `recentEntries={[]}` (present since PR #89, unrelated to the analytics key
 * mismatch) went unnoticed in the same PR that claimed to fix "Console
 * Analytics and Billing" together.
 *
 * These tests render the actual page Server Components against a mocked
 * control-plane and assert the real, non-zero values the mocked fetch
 * responses carry actually reach the rendered tree, mocking only the heavy
 * presentational children (charts, tables) down to a props dump the same
 * way __tests__/billing-profile-missing-row.test.tsx already does for
 * BillingContactForm.
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
vi.mock("@/components/analytics/usage-chart", () => ({
  UsageChart: () => <div data-testid="usage-chart" />,
}));
vi.mock("@/components/analytics/spend-chart", () => ({
  SpendChart: () => <div data-testid="spend-chart" />,
}));
vi.mock("@/components/analytics/error-chart", () => ({
  ErrorChart: () => <div data-testid="error-chart" />,
}));
vi.mock("@/components/analytics/analytics-table", () => ({
  AnalyticsTable: () => <div data-testid="analytics-table" />,
}));

vi.mock("@/components/billing/billing-overview", () => ({
  BillingOverview: ({ recentEntries }: { recentEntries: unknown[] }) => (
    <div data-testid="billing-overview">{JSON.stringify(recentEntries)}</div>
  ),
}));
vi.mock("@/components/billing/budget-alert-form", () => ({
  BudgetAlertForm: () => <div data-testid="budget-alert-form" />,
}));

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

const PROFILE_PAYLOAD = {
  owner_name: "Ada Owner",
  login_email: "ada@example.test",
  display_name: "QA Workspace",
  account_type: "personal",
  country_code: "CA",
  state_region: "ON",
  profile_setup_complete: true,
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
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

describe("app/console/analytics/page.tsx renders real, non-zero counts", () => {
  it("shows the real request/token/credit totals from the analytics endpoints, not zero", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.includes("/analytics/usage")) {
          return jsonResponse(200, {
            usage: [
              {
                group_key: "hive-auto",
                total_input_tokens: 18,
                total_output_tokens: 4,
                total_credits_spent: 3,
                request_count: 7,
              },
            ],
          });
        }
        if (url.includes("/analytics/spend")) {
          return jsonResponse(200, { spend: [] });
        }
        if (url.includes("/analytics/errors")) {
          return jsonResponse(200, { errors: [] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/analytics/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ tab: "overview", window: "24h" }),
    });
    render(page);

    expect(screen.queryByText("Unable to load analytics.")).toBeNull();
    // The four summary cards: total requests, input tokens, output tokens,
    // credits spent. Pre-fix, every one of these rendered "0" regardless of
    // what the mocked endpoints answered (issue #856); this pins the wiring
    // from the fetched rows through to the rendered totals. getByText throws
    // if the text is absent, which is the assertion.
    screen.getByText("7");
    screen.getByText("18");
    screen.getByText("4");
    screen.getByText("3");
  });
});

describe("app/console/billing/page.tsx renders real ledger rows on Overview", () => {
  it("passes real ledger entries to BillingOverview instead of a hardcoded empty array", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.endsWith("/api/v1/accounts/current/credits/balance")) {
          return jsonResponse(200, {
            posted_credits: 98000,
            reserved_credits: 0,
            available_credits: 98000,
          });
        }
        if (url.endsWith("/api/v1/accounts/current/budget")) {
          return jsonResponse(200, { threshold: null });
        }
        if (url.includes("/credits/ledger")) {
          return jsonResponse(200, {
            entries: [
              {
                id: "e1",
                entry_type: "charge",
                credits_delta: -1,
                idempotency_key: "charge-1",
                request_id: "r1",
                created_at: "2026-08-16T15:24:12Z",
              },
            ],
            next_cursor: null,
          });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/billing/page");
    const page = await mod.default({ searchParams: Promise.resolve({}) });
    render(page);

    const overview = screen.getByTestId("billing-overview");
    const entries = JSON.parse(overview.textContent ?? "[]");
    expect(entries).toHaveLength(1);
    expect(entries[0].id).toBe("e1");
  });
});
