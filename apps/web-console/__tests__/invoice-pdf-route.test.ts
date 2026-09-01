import { afterEach, describe, expect, it, vi } from "vitest";

// The proxy answered 500 for every non-2xx the control plane returned,
// including the correct 404 for an unknown invoice id, and echoed the upstream
// error text back to the browser (issue #1649). These run the real route
// against the real client with only the transport and the session mocked, so
// the status mapping is exercised end to end rather than asserted on a stub.

vi.mock("next/headers", () => ({
  cookies: async () => ({ get: () => undefined }),
}));

vi.mock("@/lib/supabase/server", () => ({
  createClient: () => ({
    auth: {
      getUser: async () => ({ data: { user: { id: "user-1" } }, error: null }),
      getSession: async () => ({
        data: { session: { access_token: "test-token" } },
      }),
    },
  }),
}));

const INVOICE_ID = "00000000-0000-0000-0000-000000000001";

async function callRoute(upstream: Response): Promise<Response> {
  process.env.CONTROL_PLANE_BASE_URL = "http://control-plane.test";
  vi.stubGlobal("fetch", vi.fn(async (): Promise<Response> => upstream));
  vi.resetModules();
  const { GET } = await import("@/app/api/invoices/[id]/pdf/route");
  return GET(
    new Request(`http://console.test/api/invoices/${INVOICE_ID}/pdf`),
    { params: Promise.resolve({ id: INVOICE_ID }) },
  );
}

describe("GET /api/invoices/[id]/pdf", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("answers 404 for an invoice id that does not exist", async () => {
    const response = await callRoute(
      new Response(JSON.stringify({ error: "invoice not found" }), {
        status: 404,
      }),
    );

    expect(response.status).toBe(404);
    expect(await response.json()).toEqual({ error: "Invoice not found" });
  });

  it("forwards a refusal status without forwarding the upstream text", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const response = await callRoute(
      new Response(
        JSON.stringify({ error: "account 42 is not a member of tenant 7" }),
        { status: 403 },
      ),
    );

    expect(response.status).toBe(403);
    const body = await response.text();
    expect(body).not.toContain("tenant");
    expect(body).toContain("You do not have access to this invoice");
  });

  it("reports an upstream failure as a gateway error, not a 404 and not raw", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const response = await callRoute(
      new Response(JSON.stringify({ error: "storage signer unreachable" }), {
        status: 500,
      }),
    );

    expect(response.status).toBe(502);
    const body = await response.text();
    expect(body).not.toContain("storage signer");
  });


  it("forwards 401 and 429 rather than reporting them as a gateway failure", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});

    // A bearer the control plane rejects. requireUser() above it only proves
    // Supabase still knows the user, so this is the only signal the console
    // gets that the answer is to sign in again (issue #1649 review).
    const unauthorized = await callRoute(
      new Response(JSON.stringify({ error: "token revoked for user 42" }), {
        status: 401,
      }),
    );
    expect(unauthorized.status).toBe(401);
    expect(await unauthorized.text()).not.toContain("user 42");

    const throttled = await callRoute(
      new Response(JSON.stringify({ error: "rate limit 30/min exceeded" }), {
        status: 429,
      }),
    );
    expect(throttled.status).toBe(429);
    expect(await throttled.text()).not.toContain("30/min");
  });

  it("calls an unusable upstream answer a gateway failure, not a missing invoice", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});

    // A redirect with no Location, and a 200 with no url field, are signer or
    // proxy defects. Both used to surface as "Invoice not found", which sends
    // the customer to support with the wrong question (issue #1649 review).
    const noLocation = await callRoute(new Response(null, { status: 302 }));
    expect(noLocation.status).toBe(502);
    expect(await noLocation.text()).not.toContain("not found");

    const noUrlField = await callRoute(
      new Response(JSON.stringify({ nothing: "useful" }), { status: 200 }),
    );
    expect(noUrlField.status).toBe(502);
    expect(await noUrlField.text()).not.toContain("not found");
  });
  it("still redirects to the signed URL on the happy path", async () => {
    const signed = "https://storage.test/invoices/inv-1.pdf?token=redacted";
    const response = await callRoute(
      new Response(null, { status: 302, headers: { Location: signed } }),
    );

    expect(response.status).toBe(302);
    expect(response.headers.get("location")).toBe(signed);
  });
});
