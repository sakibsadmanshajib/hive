import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

/**
 * Issue #494, the false-state half.
 *
 * app/console/layout.tsx read the balance with Promise.allSettled and then
 * collapsed a *rejected* balance to the number 0:
 *
 *   const currentBalance =
 *     balanceSummary?.status === "fulfilled" ? balanceSummary.value... : 0;
 *
 * 0 is below every threshold a customer can set, so a balance the console
 * could not read rendered, on every console route, as "your balance has
 * reached or dropped below your alert threshold". A surface claiming a state
 * the system does not have is this repo's dominant defect class, and the
 * layout is the one place it reached every page at once. An unknown balance
 * must be unknown.
 */

const mockRedirect = vi.fn((target: string) => {
  throw new Error(`NEXT_REDIRECT:${target}`);
});

// Only redirect() is replaced. The layout's data helpers call
// unstable_rethrow() to let Next.js's own control-flow errors past their
// catch, so a wholesale stub of this module would break them.
vi.mock("next/navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("next/navigation")>();
  return { ...actual, redirect: mockRedirect };
});

// A token whose payload carries a tenant_id, so the layout takes the normal
// console path rather than the provisioning redirect.
const TENANTED_TOKEN = `header.${Buffer.from(
  JSON.stringify({ tenant_id: "t1" }),
).toString("base64url")}.signature`;

const mockGetUser = vi.fn();
const mockGetSession = vi.fn();

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

// The verification banner reads next-intl messages through a provider this
// test has no reason to stand up. The assertions are about the budget banner
// beside it.
vi.mock("../components/verification-banner", () => ({
  VerificationBanner: () => null,
}));

const VIEWER_PAYLOAD = {
  user: { id: "u1", email: "qa@example.test", email_verified: true },
  current_account: {
    id: "a1",
    display_name: "QA Workspace",
    account_type: "personal",
    role: "owner",
  },
  memberships: [
    { account_id: "a1", display_name: "QA Workspace", role: "owner", status: "active" },
  ],
  permissions: [],
};

// The endpoint wraps the row in a "threshold" key, and decodeBudgetThreshold
// drops any row missing one of these fields. Getting either wrong makes the
// banner silently absent, which every negative case here would then pass for
// the wrong reason -- the positive case below is what keeps this fixture
// honest.
const THRESHOLD_PAYLOAD = {
  threshold: {
    id: "bt1",
    threshold_credits: 1000,
    alert_dismissed: false,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
  },
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
}

function routeFetch(balanceStatus: number, availableCredits = 5000) {
  return vi.fn(async (url: string) => {
    if (url.endsWith("/api/v1/viewer")) {
      return jsonResponse(200, VIEWER_PAYLOAD);
    }
    if (url.endsWith("/api/v1/accounts/current/credits/balance")) {
      return balanceStatus === 200
        ? jsonResponse(200, {
            posted_credits: availableCredits,
            reserved_credits: 0,
            available_credits: availableCredits,
          })
        : jsonResponse(balanceStatus, { error: "balance unavailable" });
    }
    if (url.includes("/budget")) {
      return jsonResponse(200, THRESHOLD_PAYLOAD);
    }
    return jsonResponse(404, { error: "unrouted" });
  });
}

beforeEach(() => {
  vi.resetModules();
  mockRedirect.mockClear();
  process.env.CONTROL_PLANE_BASE_URL = "http://control-plane.test";
  mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
  mockGetSession.mockResolvedValue({
    data: { session: { access_token: TENANTED_TOKEN } },
  });
});

describe("console layout budget banner", () => {
  it("does not claim the threshold was crossed when the balance is unknown", async () => {
    vi.stubGlobal("fetch", routeFetch(503));
    const { default: ConsoleLayout } = await import("../app/console/layout");

    render(await ConsoleLayout({ children: <p>page body</p> }));

    expect(screen.getByText("page body")).toBeTruthy();
    expect(screen.queryByText(/alert threshold/i)).toBeNull();
  });

  it("still warns when the balance really is under the threshold", async () => {
    vi.stubGlobal("fetch", routeFetch(200, 500));
    const { default: ConsoleLayout } = await import("../app/console/layout");

    render(await ConsoleLayout({ children: <p>page body</p> }));

    expect(screen.getByText(/alert threshold/i)).toBeTruthy();
  });

  it("stays quiet when the balance is comfortably above the threshold", async () => {
    vi.stubGlobal("fetch", routeFetch(200, 50000));
    const { default: ConsoleLayout } = await import("../app/console/layout");

    render(await ConsoleLayout({ children: <p>page body</p> }));

    expect(screen.queryByText(/alert threshold/i)).toBeNull();
  });
});
