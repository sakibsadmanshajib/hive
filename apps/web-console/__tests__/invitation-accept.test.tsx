import { beforeEach, describe, expect, it, vi } from "vitest";

const redirectError = new Error("NEXT_REDIRECT");
const mockRedirect = vi.fn(() => {
  throw redirectError;
});
const mockGetSession = vi.fn();
const mockCreateClient = vi.fn(() => ({
  auth: {
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
});
