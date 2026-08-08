/**
 * The console route that fronts the control-plane account surface takes its
 * key id straight from the URL, which is browser-controlled. `revokeApiKey`
 * interpolates that id into an upstream URL, so a value carrying a path
 * separator would retarget the request at a different control-plane path while
 * still carrying the caller's own bearer.
 *
 * These pin the properties that keep that from mattering: an id that is not a
 * UUID never reaches the upstream call, and an unauthenticated caller never
 * reaches it either.
 *
 * They also pin the checkout half, which shipped in the same route with no
 * coverage at all. The credits bound is the one that matters: credits crosses
 * this seam as a JSON number, and a value past 2^53 is already rounded by the
 * time it is parsed, so forwarding it would bill a quantity the customer never
 * chose. Deleting `credits > Number.MAX_SAFE_INTEGER` from the route turns the
 * over-2^53 rows of the table below red.
 *
 * The last property here is the one that stops a caller choosing its own
 * account: only the fields this route names are forwarded, everything else in
 * the body is dropped.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockRevokeApiKey = vi.fn();
const mockCreateApiKey = vi.fn();
const mockGetCheckoutRails = vi.fn();
const mockInitiateCheckout = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("@/lib/supabase/server", () => ({
  createClient: vi.fn(() => ({ auth: { getUser: mockGetUser } })),
}));

// The constructor order matches the real class in lib/control-plane/client.ts
// (status first). A mock whose shape drifts from the type it stands in for is
// how the route's error mapping stayed untested while it was unreachable.
vi.mock("@/lib/control-plane/client", () => ({
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
  createApiKey: (...args: unknown[]) => mockCreateApiKey(...args),
  revokeApiKey: (...args: unknown[]) => mockRevokeApiKey(...args),
  getCheckoutRails: (...args: unknown[]) => mockGetCheckoutRails(...args),
  initiateCheckout: (...args: unknown[]) => mockInitiateCheckout(...args),
}));

const VALID_KEY_ID = "c7e12110-71f6-4038-ad87-d5334143925a";

function params(path: string[]) {
  return { params: Promise.resolve({ path }) };
}

function request(body: unknown = {}): Request {
  return rawRequest(JSON.stringify(body));
}

// rawRequest sends the body bytes verbatim. Some values a real client can put
// on the wire cannot be produced by JSON.stringify: 1e999 parses back as
// Infinity, and 9007199254740993 parses back already rounded.
function rawRequest(body: string): Request {
  return new Request("http://console.test/api/v1/accounts/current/api-keys", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  });
}

function getRequest(): Request {
  return new Request("http://console.test/api/v1/accounts/current/checkout/rails");
}

describe("console proxy for the control-plane account surface", () => {
  beforeEach(() => {
    mockGetUser.mockResolvedValue({ data: { user: { id: "user-1" } }, error: null });
    mockRevokeApiKey.mockResolvedValue({ id: VALID_KEY_ID, status: "revoked" });
    mockCreateApiKey.mockResolvedValue({ id: VALID_KEY_ID, nickname: "k" });
    mockGetCheckoutRails.mockResolvedValue({ rails: [], min_credits: 1000, max_credits: 100000 });
    mockInitiateCheckout.mockResolvedValue({
      payment_intent_id: "pi-1",
      redirect_url: "https://rail.test/pay",
    });
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

  it("forwards only the fields it names on a create", async () => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(
      request({
        nickname: "k",
        expires_at: "2030-01-01",
        account_id: "11111111-1111-4111-8111-111111111111",
        scopes: ["admin"],
      }),
      params(["api-keys"]),
    );

    expect(response.status).toBe(200);
    expect(mockCreateApiKey).toHaveBeenCalledWith("k", "2030-01-01");
  });

  it("lists checkout rails for a signed-in caller", async () => {
    const { GET } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await GET(getRequest(), params(["checkout", "rails"]));

    expect(response.status).toBe(200);
    expect(mockGetCheckoutRails).toHaveBeenCalledTimes(1);
  });

  it("refuses an unauthenticated rails read before touching upstream", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
    const { GET } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await GET(getRequest(), params(["checkout", "rails"]));

    expect(response.status).toBe(401);
    expect(mockGetCheckoutRails).not.toHaveBeenCalled();
  });

  it("404s a GET that is not on the allowlist", async () => {
    const { GET } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await GET(getRequest(), params(["checkout", "rails", "extra"]));

    expect(response.status).toBe(404);
    expect(mockGetCheckoutRails).not.toHaveBeenCalled();
  });

  it("initiates a checkout with exactly the rail, credits and idempotency key", async () => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(
      request({
        rail: "bkash",
        credits: 1000,
        idempotency_key: "idem-1",
        // A client must not be able to pick its own account or its own price.
        // These three are dropped rather than forwarded.
        account_id: "22222222-2222-4222-8222-222222222222",
        workspace_id: "33333333-3333-4333-8333-333333333333",
        amount_local: 1,
      }),
      params(["checkout", "initiate"]),
    );

    expect(response.status).toBe(200);
    expect(mockInitiateCheckout).toHaveBeenCalledWith("bkash", 1000, "idem-1");
  });

  it("refuses an unauthenticated checkout before touching upstream", async () => {
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(
      request({ rail: "bkash", credits: 1000, idempotency_key: "idem-1" }),
      params(["checkout", "initiate"]),
    );

    expect(response.status).toBe(401);
    expect(mockInitiateCheckout).not.toHaveBeenCalled();
  });

  // Past 2^53 the last three rows are integers and positive, so only the
  // MAX_SAFE_INTEGER bound rejects them. 9007199254740993 has already been
  // rounded to 9007199254740992 by the time the route reads it, which is the
  // silently altered purchase quantity this bound exists to refuse.
  it.each([
    ["negative", '{"rail":"bkash","credits":-1000,"idempotency_key":"i"}'],
    ["zero", '{"rail":"bkash","credits":0,"idempotency_key":"i"}'],
    ["fractional", '{"rail":"bkash","credits":10.5,"idempotency_key":"i"}'],
    ["a string", '{"rail":"bkash","credits":"1000","idempotency_key":"i"}'],
    ["null", '{"rail":"bkash","credits":null,"idempotency_key":"i"}'],
    ["infinite", '{"rail":"bkash","credits":1e999,"idempotency_key":"i"}'],
    ["one past MAX_SAFE_INTEGER", '{"rail":"bkash","credits":9007199254740993,"idempotency_key":"i"}'],
    ["1e21", '{"rail":"bkash","credits":1e21,"idempotency_key":"i"}'],
    ["MAX_VALUE", '{"rail":"bkash","credits":1.7976931348623157e308,"idempotency_key":"i"}'],
  ])("rejects %s credits without reaching upstream", async (_label, body) => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(rawRequest(body), params(["checkout", "initiate"]));

    expect(response.status).toBe(400);
    expect(mockInitiateCheckout).not.toHaveBeenCalled();
  });

  it("rejects a checkout with no rail or no idempotency key", async () => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const noRail = await POST(
      request({ rail: "", credits: 1000, idempotency_key: "i" }),
      params(["checkout", "initiate"]),
    );
    const noKey = await POST(
      request({ rail: "bkash", credits: 1000, idempotency_key: "" }),
      params(["checkout", "initiate"]),
    );

    expect(noRail.status).toBe(400);
    expect(noKey.status).toBe(400);
    expect(mockInitiateCheckout).not.toHaveBeenCalled();
  });

  it("accepts the largest credits value that survives a round trip", async () => {
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(
      request({
        rail: "bkash",
        credits: Number.MAX_SAFE_INTEGER,
        idempotency_key: "i",
      }),
      params(["checkout", "initiate"]),
    );

    expect(response.status).toBe(200);
    expect(mockInitiateCheckout).toHaveBeenCalledWith("bkash", Number.MAX_SAFE_INTEGER, "i");
  });

  it("keeps an upstream refusal status instead of collapsing it to 502", async () => {
    const { ControlPlaneError } = await import("@/lib/control-plane/client");
    mockCreateApiKey.mockRejectedValue(new ControlPlaneError(403, "not the workspace owner"));
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(request({ nickname: "k" }), params(["api-keys"]));

    expect(response.status).toBe(403);
    expect(await response.json()).toEqual({ error: "Forbidden" });
  });

  it("reads a transport failure as 502 without echoing upstream text", async () => {
    mockInitiateCheckout.mockRejectedValue(new Error("connect ECONNREFUSED cp.internal:8081"));
    const { POST } = await import("../app/api/v1/accounts/current/[...path]/route");

    const response = await POST(
      request({ rail: "bkash", credits: 1000, idempotency_key: "idem-1" }),
      params(["checkout", "initiate"]),
    );

    expect(response.status).toBe(502);
    expect(await response.json()).toEqual({ error: "Failed to initiate checkout" });
  });
});
