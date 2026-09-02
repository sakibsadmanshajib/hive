import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

/**
 * Issue #494, round four.
 *
 * getBudget answers null only for a 404 and throws for everything else,
 * including the 403 a member gets: requireWorkspaceMembership in
 * apps/control-plane/internal/budgets/http.go gates even the GET on
 * billing.write, which authz.Policy grants to owners only.
 *
 * Collapsing that refusal into the same null the page uses for "we could not
 * read it" told a member "We could not reach the budget service", a claim
 * about a healthy service inferred from an authorization answer, and withheld
 * the read-only form they are supposed to see. It also broke the member arm of
 * tests/e2e/console-budgets.spec.ts, which is a full-stack lane; this is the
 * unit-level guard for the same behaviour.
 *
 * The 503 case is the control. Without it a page that withheld the form for
 * every failure, or one that rendered it for every failure, would satisfy
 * half of this file on its own.
 */

const mockRedirect = vi.fn((target: string) => {
  throw new Error(`NEXT_REDIRECT:${target}`);
});

// unstable_rethrow stays real: the page calls it first in its catch so a
// framework throw is never classified as a data failure, and a stub would
// pass whether or not that still holds.
vi.mock("next/navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("next/navigation")>();
  return { ...actual, redirect: mockRedirect };
});

const mockGetViewer = vi.fn();
const mockGetBudget = vi.fn();
const mockGetAccountProfile = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

class TestControlPlaneError extends Error {
  status: number;
  code: string | null;
  constructor(status: number, message: string, code: string | null = null) {
    super(message);
    this.name = "ControlPlaneError";
    this.status = status;
    this.code = code;
  }
}

vi.mock("../lib/control-plane/client", () => ({
  getViewer: mockGetViewer,
  getBudget: mockGetBudget,
  getAccountProfile: mockGetAccountProfile,
  ControlPlaneError: TestControlPlaneError,
}));

// The shell pulls in next-intl's useTranslations, which needs a provider this
// test has no reason to stand up. BudgetForm itself is left real: it is the
// thing under assertion.
vi.mock("@/components/app-shell/console-shell", () => ({
  ConsoleShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="console-shell">{children}</div>
  ),
}));

function viewerPayload(role: "owner" | "member") {
  return {
    user: { id: "u1", email: "qa@example.test", email_verified: true },
    current_account: {
      id: "a1",
      slug: "qa-workspace",
      display_name: "QA Workspace",
      account_type: "business",
      role,
    },
    memberships: [],
    permissions: [],
  };
}

async function renderBudgetPage(role: "owner" | "member") {
  mockGetViewer.mockResolvedValue(viewerPayload(role));
  mockGetAccountProfile.mockResolvedValue({ owner_name: "QA Owner" });
  const mod = await import("../app/console/billing/budget/page");
  render(await mod.default());
}

describe("app/console/billing/budget/page.tsx", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the read-only form for a member the budget read refuses", async () => {
    mockGetBudget.mockRejectedValue(
      new TestControlPlaneError(403, "workspace access denied"),
    );

    await renderBudgetPage("member");

    // What tests/e2e/console-budgets.spec.ts asserts, at the unit level.
    const softCap = document.querySelector("#budget-soft-cap");
    expect(softCap).not.toBeNull();
    expect((softCap as HTMLInputElement).disabled).toBe(true);
    expect(
      screen.getByText("Only the workspace owner can edit budget caps."),
    ).toBeTruthy();

    // A refusal is never reported as an outage.
    expect(screen.queryByText(/could not reach the budget service/i)).toBeNull();
  });

  it("says the budget is unreadable when the read really failed", async () => {
    mockGetBudget.mockRejectedValue(new Error("connect ECONNREFUSED"));

    await renderBudgetPage("member");

    expect(screen.getByText(/could not reach the budget service/i)).toBeTruthy();
    expect(document.querySelector("#budget-soft-cap")).toBeNull();
  });
});
