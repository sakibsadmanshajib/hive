import { beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockCreateClient = vi.fn(() => ({
  auth: { getUser: mockGetUser },
}));
const mockCreateInvitation = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({ get: vi.fn(() => undefined), getAll: vi.fn(() => []) })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: mockCreateClient,
}));

vi.mock("../lib/control-plane/client", () => ({
  createInvitation: mockCreateInvitation,
  // ControlPlaneError is referenced by the route for status mapping.
  ControlPlaneError: class ControlPlaneError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.name = "ControlPlaneError";
      this.status = status;
    }
  },
}));

function formRequest(email: string): Request {
  const body = new URLSearchParams({ email });
  return new Request("http://localhost:3000/api/console/members", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: body.toString(),
  });
}

describe("app/api/console/members/route.ts POST", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetUser.mockResolvedValue({
      data: { user: { id: "u1", email: "owner@hive.com" } },
      error: null,
    });
    mockCreateInvitation.mockResolvedValue(undefined);
  });

  it("proxies the invite server-side and redirects back to members on success", async () => {
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("teammate@example.com"));

    expect(mockCreateInvitation).toHaveBeenCalledWith("teammate@example.com");
    expect(res.status).toBe(303);
    const location = res.headers.get("location") ?? "";
    expect(location).toContain("/console/members");
    expect(location).toContain("invited=1");
  });

  it("rejects unauthenticated callers with 401 and never proxies", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("teammate@example.com"));

    expect(res.status).toBe(401);
    expect(mockCreateInvitation).not.toHaveBeenCalled();
  });

  it("rejects a missing or malformed email with 400 and never proxies", async () => {
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("   "));

    expect(res.status).toBe(400);
    expect(mockCreateInvitation).not.toHaveBeenCalled();
  });
});
