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
    code: string | null;
    retryAfter: string | null;
    constructor(
      status: number,
      message: string,
      code: string | null = null,
      retryAfter: string | null = null,
    ) {
      super(message);
      this.name = "ControlPlaneError";
      this.status = status;
      this.code = code;
      this.retryAfter = retryAfter;
    }
  },
}));

function jsonRequest(email: string, role?: string): Request {
  return new Request("http://localhost:3000/api/console/members", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify(role === undefined ? { email } : { email, role }),
  });
}

function formRequest(email: string, role?: string): Request {
  const body = new URLSearchParams(
    role === undefined ? { email } : { email, role },
  );
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
    // The default is the state every deployment without a relay is in, and the
    // state the demo box is in while its relay refuses every message.
    mockCreateInvitation.mockResolvedValue({
      id: "inv-1",
      token: "raw-token-value",
      delivered: false,
      delivery: "not_configured",
    });
  });

  it("proxies the invite server-side and redirects back with the real delivery outcome", async () => {
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("teammate@example.com"));

    expect(mockCreateInvitation).toHaveBeenCalledWith(
      "teammate@example.com",
      "member",
    );
    expect(res.status).toBe(303);
    const location = res.headers.get("location") ?? "";
    expect(location).toContain("/console/members");
    // Not a bare success flag. The flag it replaced was rendered as
    // "Invitation sent" while nothing in the product could send anything
    // (issue #1440).
    expect(location).toContain("invited=not_configured");
    expect(location).not.toContain("invited=1");
  });

  // THE GUARD FOR ISSUE #1440, ON THE ROUTE.
  //
  // The acceptance token is bearer-equivalent and the database keeps only its
  // hash, so a token that lands in a redirect URL is both a leak (history,
  // referrer, server logs) and the only copy in existence. It must never appear
  // in a Location header under any outcome.
  it.each(["sent", "not_configured", "failed"] as const)(
    "never puts the acceptance token in the redirect for outcome %s",
    async (delivery) => {
      mockCreateInvitation.mockResolvedValue({
        id: "inv-1",
        token: "raw-token-value",
        delivered: delivery === "sent",
        delivery,
      });
      const { POST } = await import("../app/api/console/members/route");
      const res = await POST(formRequest("teammate@example.com"));

      const location = res.headers.get("location") ?? "";
      expect(location).not.toContain("raw-token-value");
      expect(location).toContain(`invited=${delivery}`);
    },
  );

  it("returns the invitation link and the delivery outcome to a JSON caller", async () => {
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(jsonRequest("teammate@example.com"));

    expect(res.status).toBe(201);
    expect(res.headers.get("cache-control")).toContain("no-store");
    const body = (await res.json()) as {
      delivered: boolean;
      delivery: string;
      link: string;
    };
    expect(body.delivered).toBe(false);
    expect(body.delivery).toBe("not_configured");
    // The link the inviting user passes on by hand, anchored to the canonical
    // app origin rather than the request host.
    expect(body.link).toBe(
      "http://localhost:3000/invitations/accept?token=raw-token-value",
    );
  });

  it("reports a delivered invitation as delivered, and only then", async () => {
    mockCreateInvitation.mockResolvedValue({
      id: "inv-1",
      token: "raw-token-value",
      delivered: true,
      delivery: "sent",
    });
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(jsonRequest("teammate@example.com"));

    const body = (await res.json()) as { delivered: boolean; delivery: string };
    expect(body.delivered).toBe(true);
    expect(body.delivery).toBe("sent");
  });

  it("gives a JSON caller a JSON error rather than a redirect", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockCreateInvitation.mockRejectedValue(
      new ControlPlaneError(403, "internal: provider acme rejected db row 42"),
    );
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(jsonRequest("teammate@example.com"));

    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("permission");
    expect(body.error).not.toContain("provider");
  });

  // Issue #536: the invite form now carries a role selector.
  it("forwards the selected owner role", async () => {
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("coowner@example.com", "owner"));

    expect(mockCreateInvitation).toHaveBeenCalledWith(
      "coowner@example.com",
      "owner",
    );
    expect(res.status).toBe(303);
  });

  it("rejects an unsupported role with 400 and never proxies", async () => {
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("teammate@example.com", "root"));

    expect(res.status).toBe(400);
    expect(mockCreateInvitation).not.toHaveBeenCalled();
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

  it("maps a ControlPlaneError to a generic status message and never leaks upstream text", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockCreateInvitation.mockRejectedValue(
      new ControlPlaneError(403, "internal: provider acme rejected db row 42"),
    );
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("teammate@example.com"));

    expect(res.status).toBe(303);
    const location = res.headers.get("location") ?? "";
    expect(location).toContain("/console/members");
    expect(location).toContain("permission");
    // Raw upstream/internal text must never reach the browser-visible URL.
    expect(location).not.toContain("provider");
    expect(location).not.toContain("db");
  });

  it("collapses a non-ControlPlaneError to the generic message (no internal config text)", async () => {
    mockCreateInvitation.mockRejectedValue(
      new Error("CONTROL_PLANE_BASE_URL is not configured"),
    );
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(formRequest("teammate@example.com"));

    expect(res.status).toBe(303);
    const location = res.headers.get("location") ?? "";
    expect(location).not.toContain("CONTROL_PLANE");
    expect(location.toLowerCase()).toContain("could+not");
  });

  // Issue #1745. A cap refusal is not a bad request, and "please try again" is
  // the one instruction that is guaranteed to fail while the window is open.
  it("passes a cap refusal through with its own status and its wait time", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockCreateInvitation.mockRejectedValue(
      new ControlPlaneError(429, "invitation limit reached, try again in 5 minutes"),
    );
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(jsonRequest("teammate@example.com"));

    expect(res.status).toBe(429);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("5 minutes");
  });

  it("reports a counter outage as unavailable rather than as a bad request", async () => {
    const { ControlPlaneError } = await import("../lib/control-plane/client");
    mockCreateInvitation.mockRejectedValue(
      new ControlPlaneError(503, "invitations are temporarily unavailable, please try again shortly"),
    );
    const { POST } = await import("../app/api/console/members/route");
    const res = await POST(jsonRequest("teammate@example.com"));

    expect(res.status).toBe(503);
    const body = (await res.json()) as { error: string };
    expect(body.error.toLowerCase()).toContain("unavailable");
  });
});
