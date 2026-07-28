/**
 * Issue #536: role changes are proxied server-side with the caller's session,
 * and the control-plane remains the authority. This route never decides
 * authorization itself; it maps upstream refusals onto truthful, customer-safe
 * messages and never leaks upstream text into the redirect URL.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockCreateClient = vi.fn(() => ({ auth: { getUser: mockGetUser } }));
const mockUpdateMemberRole = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({ createClient: mockCreateClient }));

vi.mock("../lib/control-plane/client", () => ({
  updateMemberRole: mockUpdateMemberRole,
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

const MEMBER_ID = "22222222-2222-4222-8222-222222222222";

function formRequest(fields: Record<string, string>): Request {
  return new Request("http://localhost:3000/api/console/members/role", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(fields).toString(),
  });
}

describe("app/api/console/members/role/route.ts POST", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetUser.mockResolvedValue({
      data: { user: { id: "u1", email: "owner@hive.test" } },
      error: null,
    });
    mockUpdateMemberRole.mockResolvedValue(undefined);
  });

  it("proxies the role change and redirects back with a success flag", async () => {
    const { POST } = await import("../app/api/console/members/role/route");
    const res = await POST(formRequest({ user_id: MEMBER_ID, role: "owner" }));

    expect(mockUpdateMemberRole).toHaveBeenCalledWith(MEMBER_ID, "owner");
    expect(res.status).toBe(303);
    const location = res.headers.get("location") ?? "";
    expect(location).toContain("/console/members");
    expect(location).toContain("role_updated=1");
  });

  it("rejects unauthenticated callers with 401 and never proxies", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
    const { POST } = await import("../app/api/console/members/role/route");
    const res = await POST(formRequest({ user_id: MEMBER_ID, role: "owner" }));

    expect(res.status).toBe(401);
    expect(mockUpdateMemberRole).not.toHaveBeenCalled();
  });

  it("rejects an unsupported role with 400 and never proxies", async () => {
    const { POST } = await import("../app/api/console/members/role/route");
    const res = await POST(formRequest({ user_id: MEMBER_ID, role: "root" }));

    expect(res.status).toBe(400);
    expect(mockUpdateMemberRole).not.toHaveBeenCalled();
  });

  it("rejects a missing member id with 400 and never proxies", async () => {
    const { POST } = await import("../app/api/console/members/role/route");
    const res = await POST(formRequest({ role: "owner" }));

    expect(res.status).toBe(400);
    expect(mockUpdateMemberRole).not.toHaveBeenCalled();
  });

  it("surfaces the last-owner refusal in its own words", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockUpdateMemberRole.mockRejectedValue(
      new ControlPlaneError(409, "internal: db row 42", "last_owner_required"),
    );
    const { POST } = await import("../app/api/console/members/role/route");
    const res = await POST(formRequest({ user_id: MEMBER_ID, role: "member" }));

    const location = res.headers.get("location") ?? "";
    expect(res.status).toBe(303);
    expect(new URL(location).searchParams.get("error")).toMatch(
      /at least one owner/i,
    );
    expect(location).not.toContain("db");
  });

  it("surfaces the self-change refusal in its own words", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockUpdateMemberRole.mockRejectedValue(
      new ControlPlaneError(403, "nope", "self_role_change_forbidden"),
    );
    const { POST } = await import("../app/api/console/members/role/route");
    const res = await POST(formRequest({ user_id: MEMBER_ID, role: "member" }));

    expect(
      new URL(res.headers.get("location") ?? "").searchParams.get("error"),
    ).toMatch(/your own role/i);
  });

  it("collapses an unexpected failure to a generic message", async () => {
    mockUpdateMemberRole.mockRejectedValue(
      new Error("CONTROL_PLANE_BASE_URL is not configured"),
    );
    const { POST } = await import("../app/api/console/members/role/route");
    const res = await POST(formRequest({ user_id: MEMBER_ID, role: "member" }));

    const location = res.headers.get("location") ?? "";
    expect(location).not.toContain("CONTROL_PLANE");
    expect(
      (new URL(location).searchParams.get("error") ?? "").toLowerCase(),
    ).toContain("could not");
  });
});
