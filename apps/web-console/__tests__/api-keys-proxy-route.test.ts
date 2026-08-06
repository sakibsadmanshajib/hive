/**
 * The console route that fronts the control-plane account surface takes its
 * key id straight from the URL, which is browser-controlled. `revokeApiKey`
 * interpolates that id into an upstream URL, so a value carrying a path
 * separator would retarget the request at a different control-plane path while
 * still carrying the caller's own bearer.
 *
 * These pin the two properties that keep that from mattering: an id that is not
 * a UUID never reaches the upstream call, and an unauthenticated caller never
 * reaches it either.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockRevokeApiKey = vi.fn();
const mockCreateApiKey = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("@/lib/supabase/server", () => ({
  createClient: vi.fn(() => ({ auth: { getUser: mockGetUser } })),
}));

vi.mock("@/lib/control-plane/client", () => ({
  ControlPlaneError: class ControlPlaneError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.status = status;
    }
  },
  createApiKey: (...args: unknown[]) => mockCreateApiKey(...args),
  revokeApiKey: (...args: unknown[]) => mockRevokeApiKey(...args),
  getCheckoutRails: vi.fn(),
  initiateCheckout: vi.fn(),
}));

const VALID_KEY_ID = "c7e12110-71f6-4038-ad87-d5334143925a";

function params(path: string[]) {
  return { params: Promise.resolve({ path }) };
}

function request(body: unknown = {}): Request {
  return new Request("http://console.test/api/v1/accounts/current/api-keys", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

describe("console proxy for the control-plane account surface", () => {
  beforeEach(() => {
    mockGetUser.mockResolvedValue({ data: { user: { id: "user-1" } }, error: null });
    mockRevokeApiKey.mockResolvedValue({ id: VALID_KEY_ID, status: "revoked" });
    mockCreateApiKey.mockResolvedValue({ id: VALID_KEY_ID, nickname: "k" });
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("revokes a key when the id is a UUID", async () => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(request(), params(["api-keys", VALID_KEY_ID, "revoke"]));

    expect(response.status).toBe(200);
    expect(mockRevokeApiKey).toHaveBeenCalledWith(VALID_KEY_ID);
  });

  it.each([
    ["path traversal", "../../../internal/apikeys/resolve"],
    ["encoded traversal", "..%2F..%2Fadmin"],
    ["not a uuid", "totally-not-a-uuid"],
    ["empty-ish", "."],
  ])("refuses a %s key id without calling upstream", async (_label, keyId) => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(request(), params(["api-keys", keyId, "revoke"]));

    expect(response.status).toBe(404);
    expect(mockRevokeApiKey).not.toHaveBeenCalled();
  });

  it("refuses an unauthenticated caller before touching upstream", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(request(), params(["api-keys", VALID_KEY_ID, "revoke"]));

    expect(response.status).toBe(401);
    expect(mockRevokeApiKey).not.toHaveBeenCalled();
  });

  it("rejects a create with no nickname before touching upstream", async () => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(request({ nickname: "   " }), params(["api-keys"]));

    expect(response.status).toBe(400);
    expect(mockCreateApiKey).not.toHaveBeenCalled();
  });

  it("404s an operation that is not on the allowlist", async () => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(request(), params(["members", "someone", "promote"]));

    expect(response.status).toBe(404);
  });
});
