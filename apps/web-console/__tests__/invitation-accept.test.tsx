import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const redirectError = new Error("NEXT_REDIRECT");
const mockRedirect = vi.fn(() => {
  throw redirectError;
});
const mockGetSession = vi.fn();
const mockGetUser = vi.fn();
const mockCreateClient = vi.fn(() => ({
  auth: {
    getUser: mockGetUser,
    getSession: mockGetSession,
  },
}));

vi.mock("next/navigation", () => ({
  redirect: mockRedirect,
}));

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: mockCreateClient,
}));

describe("app/invitations/accept/page.tsx", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
    mockGetUser.mockResolvedValue({
      data: {
        user: { id: "test-user", email: "test@hive.com" },
      },
      error: null,
    });
    mockGetSession.mockResolvedValue({
      data: {
        session: {
          access_token: "test-token",
        },
      },
    });
  });

  it("does not swallow the redirect when invitation acceptance succeeds", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
    });
    vi.stubGlobal("fetch", fetchMock);

    const mod = await import("../app/invitations/accept/page");

    await expect(
      mod.default({
        searchParams: Promise.resolve({ token: "invite-token-1" }),
      })
    ).rejects.toBe(redirectError);

    expect(fetchMock).toHaveBeenCalledWith(
      "http://localhost:8081/api/v1/invitations/accept",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          Authorization: "Bearer test-token",
        }),
      })
    );
    expect(mockRedirect).toHaveBeenCalledWith("/console/members?joined=1");
  });

  // --- issue #534: the token must survive the sign-in / sign-up round trip ---

  it("carries the token through the sign-in bounce for a signed-out invitee", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const mod = await import("../app/invitations/accept/page");

    await expect(
      mod.default({
        searchParams: Promise.resolve({ token: "invite-token-1" }),
      })
    ).rejects.toBe(redirectError);

    expect(mockRedirect).toHaveBeenCalledWith(
      `/auth/sign-in?next=${encodeURIComponent(
        "/invitations/accept?token=invite-token-1"
      )}`
    );
    // The bounce must never post the token upstream unauthenticated.
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("carries the token through the sign-in bounce when the session is missing", async () => {
    mockGetSession.mockResolvedValue({ data: { session: null } });

    const mod = await import("../app/invitations/accept/page");

    await expect(
      mod.default({
        searchParams: Promise.resolve({ token: "invite-token-2" }),
      })
    ).rejects.toBe(redirectError);

    expect(mockRedirect).toHaveBeenCalledWith(
      `/auth/sign-in?next=${encodeURIComponent(
        "/invitations/accept?token=invite-token-2"
      )}`
    );
  });

  it("still bounces to plain sign-in when no token was supplied", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });

    const mod = await import("../app/invitations/accept/page");

    await expect(
      mod.default({ searchParams: Promise.resolve({}) })
    ).rejects.toBe(redirectError);

    expect(mockRedirect).toHaveBeenCalledWith(
      "/auth/sign-in?next=%2Finvitations%2Faccept"
    );
  });

  // --- issue #534: truthful, distinct copy per token lifecycle state ---

  async function renderAcceptFailure(
    status: number,
    body: Record<string, string>
  ) {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status,
        text: async () => JSON.stringify(body),
      })
    );

    const mod = await import("../app/invitations/accept/page");
    const ui = await mod.default({
      searchParams: Promise.resolve({ token: "invite-token-3" }),
    });
    render(ui);
  }

  it("tells an expired invitee that the link expired and a new one is needed", async () => {
    await renderAcceptFailure(410, {
      error: "this invitation has expired",
      code: "invitation_expired",
    });

    expect(screen.getByText(/expired/i)).toBeTruthy();
    expect(screen.getByText(/ask.*new invitation/i)).toBeTruthy();
  });

  it("tells an already-accepted invitee the invite was used, without asking for a fresh link", async () => {
    await renderAcceptFailure(409, {
      error: "this invitation has already been accepted",
      code: "invitation_already_accepted",
    });

    expect(screen.getByText(/already been accepted/i)).toBeTruthy();
    expect(screen.queryByText(/new invitation/i)).toBeNull();
    // A workspace switcher pointer is the real next action here.
    expect(screen.getByText(/workspace switcher/i)).toBeTruthy();
  });

  it("tells a wrong-account invitee to sign in with the invited address", async () => {
    await renderAcceptFailure(403, {
      error: "this invitation was sent to a different email address",
      code: "invitation_email_mismatch",
    });

    expect(screen.getByText(/different email address/i)).toBeTruthy();
    expect(screen.getAllByText(/test@hive.com/).length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: /sign out/i })).toBeTruthy();
    expect(screen.queryByText(/new invitation/i)).toBeNull();
  });

  it("tells an existing member they already belong to the workspace", async () => {
    await renderAcceptFailure(409, {
      error: "you are already a member of this workspace",
      code: "invitation_already_member",
    });

    expect(screen.getByText(/already in this workspace/i)).toBeTruthy();
    expect(screen.getByText(/workspace switcher/i)).toBeTruthy();
    expect(screen.queryByText(/new invitation/i)).toBeNull();
  });

  it("tells an unknown-token visitor the link is not valid", async () => {
    await renderAcceptFailure(404, {
      error: "this invitation link is not valid",
      code: "invitation_not_found",
    });

    expect(screen.getByText(/not valid/i)).toBeTruthy();
  });
});
