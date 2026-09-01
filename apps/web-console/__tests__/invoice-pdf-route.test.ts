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
    expect(body).toContain("You do not have access to this invoice.");
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

  it("still redirects to the signed URL on the happy path", async () => {
    const signed = "https://storage.test/invoices/inv-1.pdf?token=redacted";
    const response = await callRoute(
      new Response(null, { status: 302, headers: { Location: signed } }),
    );

    expect(response.status).toBe(302);
    expect(response.headers.get("location")).toBe(signed);
  });
});
