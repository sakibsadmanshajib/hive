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
  // Renders the window it was handed, so a page test can assert the control
  // strip agrees with the window the fetches actually asked for.
  AnalyticsControls: (props: { currentWindow: string }) => (
    <div data-testid="analytics-controls">{props.currentWindow}</div>
  ),
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

// A usage_events row the client's decoder accepts: it rejects any row missing
// one of the eight required string fields, so the tests that need a real
// cache sample build rows here rather than inline.
function usageEvent(id: string, createdAt: string, cacheRead: number) {
  return {
    id,
    request_id: id,
    request_attempt_id: id,
    event_type: "completed",
    endpoint: "/v1/chat/completions",
    model_alias: "hive-auto",
    status: "completed",
    created_at: createdAt,
    input_tokens: 1000,
    output_tokens: 100,
    cache_read_tokens: cacheRead,
    hive_credit_delta: -500,
  };
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
                // Rendered by the Total spend tile through formatCreditAmount
                // (issue #1694): Hive credits, grouped, with no currency.
                total_credits_spent: 3_000_000_000,
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
        if (url.includes("/usage-events")) {
          return jsonResponse(200, { events: [], next_cursor: null });
        }
        if (url.includes("/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/analytics/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ tab: "overview", window: "24h" }),
    });
    render(page);

    // Regex, not a string: the rendered banner is the single text node
    // "Unable to load analytics. Refresh to try again." and queryByText
    // matches the whole normalized string exactly by default, so the string
    // form returned null whether or not the banner was there.
    expect(screen.queryByText(/Unable to load analytics/)).toBeNull();
    // The four summary cards: total requests, input tokens, output tokens,
    // total spend. Pre-fix, every one of these rendered "0" regardless of
    // what the mocked endpoints answered (issue #856); this pins the wiring
    // from the fetched rows through to the rendered totals. getByText throws
    // if the text is absent, which is the assertion.
    screen.getByText("7");
    screen.getByText("18");
    screen.getByText("4");
    screen.getByText("3,000,000,000 credits");
  });

  it("renders 'Unavailable' for the tile deltas and the top-keys panel when their own fetches fail, never the same 'No prior data' / empty text a real zero would render", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.includes("/analytics/usage")) {
          // The prior-period call is distinguished by an explicit `from=`
          // bound (fetchPreviousUsage); the current-period call carries
          // `window=` instead (fetchMain). Only the prior-period call fails
          // here, simulating a real control-plane hiccup on that one leg.
          if (url.includes("from=")) {
            return jsonResponse(500, { error: "boom" });
          }
          return jsonResponse(200, {
            usage: [
              {
                group_key: "hive-auto",
                total_input_tokens: 18,
                total_output_tokens: 4,
                total_credits_spent: 3_000_000_000,
                request_count: 7,
              },
            ],
          });
        }
        if (url.includes("/analytics/spend")) {
          // fetchTopKeys asks group_by=api_key; fetchMain asks group_by
          // whatever the page's own default is (model). Only the former
          // fails.
          if (url.includes("group_by=api_key")) {
            return jsonResponse(500, { error: "boom" });
          }
          return jsonResponse(200, { spend: [] });
        }
        if (url.includes("/analytics/errors")) {
          return jsonResponse(200, { errors: [] });
        }
        if (url.includes("/usage-events")) {
          return jsonResponse(200, { events: [], next_cursor: null });
        }
        if (url.includes("/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/analytics/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ tab: "overview", window: "24h" }),
    });
    render(page);

    // Total requests, input tokens, output tokens, total spend, and blended
    // price all derive their delta from the same failed prior-period fetch,
    // so all five render the fetch-failure state.
    expect(screen.getAllByText("Unavailable").length).toBeGreaterThanOrEqual(5);
    // Never the real-zero-previous-period text: that would tell an account
    // with real prior spend that it measurably had none.
    expect(screen.queryByText("No prior data")).toBeNull();

    // Top API keys panel: distinct "Unavailable." (with the period, from
    // TopApiKeysCard) from the panel's own real-empty-result text.
    screen.getByText("Unavailable.");
    expect(
      screen.queryByText("No API keys with spend in this window."),
    ).toBeNull();
  });

  it("renders the page-level error banner, and no overview tiles, when the tab's own primary fetch fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.includes("/analytics/usage")) {
          return jsonResponse(500, { error: "boom" });
        }
        if (url.includes("/analytics/spend")) {
          return jsonResponse(200, { spend: [] });
        }
        if (url.includes("/analytics/errors")) {
          return jsonResponse(200, { errors: [] });
        }
        if (url.includes("/usage-events")) {
          return jsonResponse(200, { events: [], next_cursor: null });
        }
        if (url.includes("/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/analytics/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ tab: "overview", window: "24h" }),
    });
    render(page);

    // The banner is one text node ending in "Refresh to try again.", so only
    // a substring matcher can see it at all.
    screen.getByText(/Unable to load analytics/);
    // And the tiles are gone rather than sitting next to it rendering zeros
    // an account could read as real.
    expect(screen.queryByTestId("total-spend")).toBeNull();
    expect(screen.queryByTestId("cache-hit-rate")).toBeNull();
  });

  it("renders sparkline captions off a real sample, a rise off a measured zero, and 'No prior data' for a price that has no prior figure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
        if (url.endsWith("/api/v1/accounts/current/profile")) {
          return jsonResponse(200, PROFILE_PAYLOAD);
        }
        if (url.includes("/analytics/usage")) {
          // The prior-period call is the one carrying explicit from/to
          // bounds. It succeeds here, and returns a genuinely empty prior
          // period rather than failing.
          if (url.includes("from=")) {
            return jsonResponse(200, { usage: [] });
          }
          return jsonResponse(200, {
            usage: [
              {
                group_key: "hive-auto",
                total_input_tokens: 18,
                total_output_tokens: 4,
                // Rendered by the Total spend tile through formatCreditAmount
                // (issue #1694): Hive credits, grouped, with no currency.
                total_credits_spent: 3_000_000_000,
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
        if (url.includes("/usage-events")) {
          const events = [
            usageEvent("ev1", "2026-08-25T09:00:00Z", 200),
            usageEvent("ev2", "2026-08-25T11:00:00Z", 400),
          ];
          return jsonResponse(200, { events, next_cursor: null });
        }
        if (url.includes("/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/analytics/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ tab: "overview", window: "24h" }),
    });
    render(page);

    // Sparkline caption: visible text, one per tile that carries a trend,
    // stating the sample it covers. Never absent, and never silent about a
    // sample being smaller than the window the headline number aggregates.
    const captions = screen.getAllByText("Trend across all 2 requests this window");
    expect(captions.length).toBeGreaterThanOrEqual(5);
    // The prior period came back genuinely empty, so requests, input tokens,
    // output tokens and spend each rose off a measured zero. That is a rise,
    // not the absence of a prior period, and it reads differently.
    const rises = screen.getAllByText(/from zero in the prior period/);
    expect(rises.length).toBeGreaterThanOrEqual(4);
    // Blended price is the exception: a prior period that served no tokens
    // has no price at all, so that one tile says there is no prior figure
    // rather than claiming a rise off zero credits per million.
    screen.getByText("No prior data");
    expect(screen.queryByText("Unavailable")).toBeNull();
  });

  it("resolves a prototype-colliding window value to the 7d default instead of 500ing or labelling 7d rows as that window", async () => {
    const urls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        urls.push(url);
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
                // Rendered by the Total spend tile through formatCreditAmount
                // (issue #1694): Hive credits, grouped, with no currency.
                total_credits_spent: 3_000_000_000,
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
        if (url.includes("/usage-events")) {
          return jsonResponse(200, { events: [], next_cursor: null });
        }
        if (url.includes("/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/analytics/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ tab: "overview", window: "toString" }),
    });
    render(page);

    // Live-captured before the fix: this exact query string returned HTTP 500,
    // because both the span lookup and the membership check walked the
    // prototype chain and resolved "toString" to an inherited, always-truthy
    // function.
    expect(screen.queryByText(/Unable to load analytics/)).toBeNull();
    screen.getByText("7");

    // And the crafted value never reaches the backend, which would have
    // answered it with its own 7d rows while every panel here kept saying
    // this window. The page resolves the window first and asks for 7d.
    expect(urls.some((u) => u.includes("window=toString"))).toBe(false);
    expect(urls.some((u) => u.includes("window=7d"))).toBe(true);
  });

  it("serves a stale custom range URL as 7d and says 7d, never as the range it cannot fetch", async () => {
    const urls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        urls.push(url);
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
                // Rendered by the Total spend tile through formatCreditAmount
                // (issue #1694): Hive credits, grouped, with no currency.
                total_credits_spent: 3_000_000_000,
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
        if (url.includes("/usage-events")) {
          return jsonResponse(200, { events: [], next_cursor: null });
        }
        if (url.includes("/api-keys")) {
          return jsonResponse(200, { items: [] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    const mod = await import("../app/console/analytics/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ tab: "overview", window: "custom:2026-01-01:2026-01-05" }),
    });
    render(page);

    // The Custom control that produced this value is gone (issue #1338): no
    // fetch on this page understood it, so the page served 7d rows under a
    // heading naming the range. A stale link or a hand-typed URL can still
    // carry one, and it resolves to 7d with the picker showing 7d, which is
    // the window that was actually fetched.
    expect(screen.queryByText(/Unable to load analytics/)).toBeNull();
    screen.getByText("7");
    expect(urls.some((u) => u.includes("custom%3A"))).toBe(false);
    expect(urls.some((u) => u.includes("custom:"))).toBe(false);
    expect(urls.some((u) => u.includes("window=7d"))).toBe(true);
    // The control strip agrees with what was fetched rather than showing a
    // selection nothing honoured.
    expect(screen.getByTestId("analytics-controls").textContent).toBe("7d");
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
