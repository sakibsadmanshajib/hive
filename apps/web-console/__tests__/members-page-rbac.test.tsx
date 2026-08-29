/**
 * Issues #535 and #536: the members page must show the outcome of an invite
 * (success and failure), let an owner pick a role at invite time, let an owner
 * change an existing member's role, identify members by something human, and
 * state the real reason whenever a control is disabled.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const mockRedirect = vi.fn(() => {
  throw new Error("NEXT_REDIRECT");
});
const mockGetUser = vi.fn();
const mockGetSession = vi.fn();
const mockCreateClient = vi.fn(() => ({
  auth: { getUser: mockGetUser, getSession: mockGetSession },
}));
const mockGetViewer = vi.fn();
const mockGetMembers = vi.fn();
const mockGetAccountProfile = vi.fn();

// useRouter is here for the invite panel, which is a client component and calls
// router.refresh() after issuing an invitation. Mocking the module rather than
// the panel keeps the real form in the tree, so the role-selector assertions
// below still test the thing they name.
vi.mock("next/navigation", () => ({
  redirect: mockRedirect,
  useRouter: () => ({ refresh: vi.fn() }),
}));

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({ createClient: mockCreateClient }));

vi.mock("../lib/control-plane/client", () => ({
  getViewer: mockGetViewer,
  getMembers: mockGetMembers,
  getAccountProfile: mockGetAccountProfile,
  ControlPlaneError: class ControlPlaneError extends Error {
    status: number;
    code: string | null;
    constructor(status: number, message: string, code: string | null = null) {
      super(message);
      this.name = "ControlPlaneError";
      this.status = status;
      this.code = code;
    }
  },
}));

// The shell pulls in next-intl's useTranslations, which needs a provider this
// test has no reason to stand up.
vi.mock("@/components/app-shell/console-shell", () => ({
  ConsoleShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="console-shell">{children}</div>
  ),
}));

const OWNER_ID = "11111111-1111-4111-8111-111111111111";
const MEMBER_ID = "22222222-2222-4222-8222-222222222222";
const SECOND_OWNER_ID = "33333333-3333-4333-8333-333333333333";

function viewerPayload(permissions: string[]) {
  return {
    user: { id: OWNER_ID, email: "owner@example.test", email_verified: true },
    current_account: {
      id: "a1",
      slug: "qa-workspace",
      display_name: "QA Workspace",
      account_type: "business",
      role: "owner",
    },
    memberships: [],
    permissions,
  };
}

async function renderMembersPage(
  params: Record<string, string>,
  permissions: string[] = ["members.invite", "members.manage"],
) {
  mockGetViewer.mockResolvedValue(viewerPayload(permissions));
  const mod = await import("../app/console/members/page");
  const ui = await mod.default({ searchParams: Promise.resolve(params) });
  render(ui);
}

describe("app/console/members/page.tsx", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetUser.mockResolvedValue({
      data: { user: { id: OWNER_ID, email: "owner@example.test" } },
      error: null,
    });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "session-token" } },
    });
    mockGetAccountProfile.mockResolvedValue({ owner_name: "Ada Owner" });
    mockGetMembers.mockResolvedValue({
      invitations: [
        {
          id: "inv-1",
          email: "invitee@example.test",
          role: "member",
          status: "pending",
          expires_at: "2026-09-01T09:30:00Z",
          created_at: "2026-08-29T09:30:00Z",
        },
      ],
      members: [
      {
        user_id: OWNER_ID,
        email: "owner@example.test",
        role: "owner",
        status: "active",
      },
      {
        user_id: SECOND_OWNER_ID,
        email: "coowner@example.test",
        role: "owner",
        status: "active",
      },
      {
        user_id: MEMBER_ID,
        email: "teammate@example.test",
        role: "member",
        status: "active",
      },
      ],
    });
  });

  // --- #535: invite feedback is rendered ---

  // Issue #1440. The old assertion here was that `invited=1` renders
  // "Invitation sent". It passed for months while nothing in the product could
  // send anything, which is what a test asserting the copy rather than the
  // outcome buys you.
  it("reports a real delivery as a delivery", async () => {
    await renderMembersPage({ invited: "sent" });
    expect(screen.getByRole("status").textContent).toMatch(/emailed an invitation/i);
  });

  it("never claims a send when nothing was sent", async () => {
    await renderMembersPage({ invited: "not_configured" });
    const banner = screen.getByRole("status").textContent ?? "";
    expect(banner).toMatch(/nothing was emailed/i);
    expect(banner).not.toMatch(/invitation sent|we emailed/i);
    expect(banner).toMatch(/new link/i);
  });

  it("treats the retired success flag as a failure rather than resurrecting the claim", async () => {
    await renderMembersPage({ invited: "1" });
    const banner = screen.getByRole("status").textContent ?? "";
    expect(banner).not.toMatch(/invitation sent|we emailed/i);
  });

  it("shows an outstanding invitation in the members table", async () => {
    await renderMembersPage({});
    expect(screen.getByText("invitee@example.test")).toBeTruthy();
    expect(screen.getByText("Invited")).toBeTruthy();
  });

  it("renders the joined-workspace confirmation", async () => {
    await renderMembersPage({ joined: "1" });
    expect(screen.getByRole("status").textContent).toMatch(/joined/i);
  });

  it("renders a failed invite as an unmistakable alert", async () => {
    await renderMembersPage({
      error: "You do not have permission to invite members.",
    });
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toMatch(/do not have permission/i);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("renders the role-updated confirmation", async () => {
    await renderMembersPage({ role_updated: "1" });
    expect(screen.getByRole("status").textContent).toMatch(/role updated/i);
  });

  it("shows no banner at all with no feedback params", async () => {
    await renderMembersPage({});
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  // --- #536: role selection, role editing, human identity, truthful reasons ---

  it("offers a role selector on the invite form", async () => {
    await renderMembersPage({});
    const select = screen.getByLabelText("Role");
    expect(select.getAttribute("name")).toBe("role");
    const values = Array.from(
      select.querySelectorAll("option"),
      (option) => option.getAttribute("value"),
    );
    expect(values).toEqual(["member", "owner"]);
  });

  it("identifies members by email instead of a raw UUID", async () => {
    await renderMembersPage({});
    expect(screen.getByText("teammate@example.test")).toBeTruthy();
    expect(screen.queryByText(MEMBER_ID)).toBeNull();
  });

  it("lets an owner change another member's role", async () => {
    await renderMembersPage({});
    const select = screen.getByLabelText(/role for teammate@example.test/i);
    expect(select.getAttribute("name")).toBe("role");
    const form = select.closest("form");
    expect(form?.getAttribute("action")).toBe("/api/console/members/role");
    expect(form?.getAttribute("method")?.toLowerCase()).toBe("post");
    expect(
      form?.querySelector('input[name="user_id"]')?.getAttribute("value"),
    ).toBe(MEMBER_ID);
  });

  it("does not offer a role editor for the viewer's own membership", async () => {
    await renderMembersPage({});
    expect(screen.queryByLabelText(/role for owner@example.test/i)).toBeNull();
    expect(screen.getByText(/cannot change your own role/i)).toBeTruthy();
  });

  it("states the last-owner rule instead of offering to demote a sole owner", async () => {
    mockGetMembers.mockResolvedValue({
      invitations: [],
      members: [
        {
          user_id: OWNER_ID,
          email: "owner@example.test",
          role: "owner",
          status: "active",
        },
        {
          user_id: MEMBER_ID,
          email: "teammate@example.test",
          role: "member",
          status: "active",
        },
      ],
    });
    await renderMembersPage({});
    expect(screen.queryByLabelText(/role for owner@example.test/i)).toBeNull();
    expect(
      screen.getByText(/must keep at least one owner/i),
    ).toBeTruthy();
  });

  it("states the permission gate, not email verification, on a disabled invite control", async () => {
    await renderMembersPage({}, ["analytics.view"]);
    const button = screen.getByRole("button", { name: /create invitation/i });
    expect(button.hasAttribute("disabled")).toBe(true);
    expect(
      screen.getByText(/only workspace owners can invite teammates/i),
    ).toBeTruthy();
    expect(screen.queryByText(/verify your email/i)).toBeNull();
  });

  // The control-plane restricts the member list to owners. A member landing here
  // used to hit the console error boundary because the refusal was thrown.
  it("explains an owner-only member list instead of crashing the page", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockGetMembers.mockRejectedValue(
      new ControlPlaneError(403, "email must be verified before accessing members"),
    );
    await renderMembersPage({}, ["analytics.view"]);

    expect(screen.getByText(/member list is owner-only/i)).toBeTruthy();
    expect(screen.queryByText(/something went wrong/i)).toBeNull();
  });

  it("still surfaces a non-permission member-list failure", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockGetMembers.mockRejectedValue(new ControlPlaneError(500, "upstream down"));
    mockGetViewer.mockResolvedValue(viewerPayload(["members.invite"]));
    const mod = await import("../app/console/members/page");

    await expect(
      mod.default({ searchParams: Promise.resolve({}) }),
    ).rejects.toThrow(/upstream down/);
  });

  it("states the permission gate on role controls for a viewer without members.manage", async () => {
    await renderMembersPage({}, ["members.invite"]);
    expect(screen.queryByLabelText(/role for teammate@example.test/i)).toBeNull();
    // Every row says why, not just one of them.
    expect(
      screen.getAllByText(/only workspace owners can change roles/i),
    ).toHaveLength(3);
  });
});
