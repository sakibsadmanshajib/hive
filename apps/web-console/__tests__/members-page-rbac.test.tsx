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

vi.mock("next/navigation", () => ({ redirect: mockRedirect }));

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
    mockGetMembers.mockResolvedValue([
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
    ]);
  });

  // --- #535: invite feedback is rendered ---

  it("renders the invite-sent confirmation", async () => {
    await renderMembersPage({ invited: "1" });
    expect(screen.getByRole("status").textContent).toMatch(/invitation sent/i);
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
    mockGetMembers.mockResolvedValue([
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
    ]);
    await renderMembersPage({});
    expect(screen.queryByLabelText(/role for owner@example.test/i)).toBeNull();
    expect(
      screen.getByText(/must keep at least one owner/i),
    ).toBeTruthy();
  });

  it("states the permission gate, not email verification, on a disabled invite control", async () => {
    await renderMembersPage({}, ["analytics.view"]);
    const button = screen.getByRole("button", { name: /send invite/i });
    expect(button.hasAttribute("disabled")).toBe(true);
    expect(
      screen.getByText(/only workspace owners can invite teammates/i),
    ).toBeTruthy();
    expect(screen.queryByText(/verify your email/i)).toBeNull();
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
