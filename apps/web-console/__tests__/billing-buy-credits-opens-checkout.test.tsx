/**
 * Issue #1386, third layer. "Buy credits" is a `Link` to
 * `/console/billing?action=buy` (components/billing/billing-overview.tsx), but
 * nothing on the billing page read that parameter and `CheckoutModal` had no
 * import anywhere in the app, so the click navigated back to the same page and
 * no modal ever appeared. The checkout rails 500 sat behind a control that
 * could not reach it.
 *
 * Renders the real billing page Server Component against a mocked
 * control-plane, mocking only the heavy presentational children down to a props
 * dump, the same way __tests__/analytics-billing-page-wiring.test.tsx does.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

const mockGetUser = vi.fn();
const mockGetSession = vi.fn();

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

vi.mock("@/components/billing/billing-overview", () => ({
  BillingOverview: () => <div data-testid="billing-overview" />,
}));

vi.mock("@/components/billing/budget-alert-form", () => ({
  BudgetAlertForm: () => <div data-testid="budget-alert-form" />,
}));

// The launcher is the seam under test: assert it is mounted, without dragging
// the modal's own fetch into a page test. It takes no props: the account's
// country used to decide whether the modal showed a price at all, and the price
// now comes from the rail the payer selects (issue #1737).
vi.mock("@/components/billing/checkout-launcher", () => ({
  CheckoutLauncher: () => <div data-testid="checkout-launcher" />,
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
  memberships: [],
  permissions: [],
};

const PROFILE_PAYLOAD = {
  owner_name: "QA Tester",
  login_email: "qa@example.test",
  display_name: "QA Workspace",
  account_type: "personal",
  country_code: "BD",
  state_region: "",
  profile_setup_complete: true,
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function stubControlPlane(): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.endsWith("/api/v1/viewer")) return jsonResponse(200, VIEWER_PAYLOAD);
      if (url.endsWith("/api/v1/accounts/current/profile")) {
        return jsonResponse(200, PROFILE_PAYLOAD);
      }
      if (url.endsWith("/api/v1/accounts/current/credits/balance")) {
        return jsonResponse(200, {
          posted_credits: 455383073,
          reserved_credits: 0,
          available_credits: 455383073,
        });
      }
      if (url.endsWith("/api/v1/accounts/current/budget")) {
        return jsonResponse(200, { threshold: null });
      }
      if (url.includes("/credits/ledger")) {
        return jsonResponse(200, { entries: [], next_cursor: null });
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
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

describe("app/console/billing/page.tsx mounts the checkout modal", () => {
  it("mounts the checkout launcher for the URL the Buy credits link points at", async () => {
    stubControlPlane();

    const mod = await import("../app/console/billing/page");
    const page = await mod.default({
      searchParams: Promise.resolve({ action: "buy" }),
    });
    render(page);

    // Without this the "Buy credits" link is inert: it navigates to this exact
    // URL and the page renders identically to the one it left.
    expect(screen.getByTestId("checkout-launcher")).toBeTruthy();
  });

  it("does not mount it for a plain visit to the billing page", async () => {
    stubControlPlane();

    const mod = await import("../app/console/billing/page");
    const page = await mod.default({ searchParams: Promise.resolve({}) });
    render(page);

    expect(screen.queryByTestId("checkout-launcher")).toBeNull();
  });
});
