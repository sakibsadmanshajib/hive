import { beforeEach, describe, expect, it, vi } from "vitest";

// The SSLCommerz browser return is a cross-site form POST, which a Next.js page
// cannot serve. This route absorbs the POST and redirects the browser onward to
// the console return page. It must never settle anything and must never trust
// an outcome supplied in the request.

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({ get: vi.fn(() => undefined), getAll: vi.fn(() => []) })),
}));

const INTENT = "123e4567-e89b-12d3-a456-426614174000";

function formPost(body: Record<string, string>, search = ""): Request {
  return new Request(`http://localhost:3000/api/payments/return/sslcommerz${search}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      host: "localhost:3000",
    },
    body: new URLSearchParams(body).toString(),
  });
}

describe("app/api/payments/return/sslcommerz/route.ts", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("redirects the browser to the console return page with the intent id", async () => {
    const { POST } = await import("../app/api/payments/return/sslcommerz/route");
    const res = await POST(formPost({ tran_id: INTENT, status: "VALID" }, `?intent=${INTENT}`));

    expect(res.status).toBe(303);
    const location = new URL(res.headers.get("location") ?? "", "http://localhost:3000");
    expect(location.pathname).toBe("/console/billing/checkout/return");
    expect(location.searchParams.get("intent")).toBe(INTENT);
    expect(location.searchParams.get("rail")).toBe("sslcommerz");
  });

  it("falls back to the SSLCommerz tran_id when no intent query is present", async () => {
    const { POST } = await import("../app/api/payments/return/sslcommerz/route");
    const res = await POST(formPost({ tran_id: INTENT }));

    const location = new URL(res.headers.get("location") ?? "", "http://localhost:3000");
    expect(location.searchParams.get("intent")).toBe(INTENT);
  });

  it("never forwards a caller-supplied outcome or status as state", async () => {
    const { POST } = await import("../app/api/payments/return/sslcommerz/route");
    const res = await POST(
      formPost(
        { tran_id: INTENT, status: "VALID", amount: "999.00" },
        `?intent=${INTENT}&outcome=success&state=success&hint=success`,
      ),
    );

    const location = new URL(res.headers.get("location") ?? "", "http://localhost:3000");
    expect(location.searchParams.get("outcome")).toBeNull();
    expect(location.searchParams.get("state")).toBeNull();
    // Only the cancelled copy hint is ever propagated.
    expect(location.searchParams.get("hint")).toBeNull();
  });

  it("propagates the cancelled copy hint", async () => {
    const { POST } = await import("../app/api/payments/return/sslcommerz/route");
    const res = await POST(formPost({ tran_id: INTENT }, `?intent=${INTENT}&hint=cancelled`));

    const location = new URL(res.headers.get("location") ?? "", "http://localhost:3000");
    expect(location.searchParams.get("hint")).toBe("cancelled");
  });

  it("sends the customer to billing when no usable intent id is present", async () => {
    const { POST } = await import("../app/api/payments/return/sslcommerz/route");
    const res = await POST(formPost({ tran_id: "not-a-uuid" }, "?intent=also-not-a-uuid"));

    expect(res.status).toBe(303);
    const location = new URL(res.headers.get("location") ?? "", "http://localhost:3000");
    expect(location.pathname).toBe("/console/billing");
  });

  it("redirects on GET too, since a provider may return the browser without a POST", async () => {
    const { GET } = await import("../app/api/payments/return/sslcommerz/route");
    const res = await GET(
      new Request(`http://localhost:3000/api/payments/return/sslcommerz?intent=${INTENT}`, {
        headers: { host: "localhost:3000" },
      }),
    );

    expect(res.status).toBe(303);
    const location = new URL(res.headers.get("location") ?? "", "http://localhost:3000");
    expect(location.pathname).toBe("/console/billing/checkout/return");
    expect(location.searchParams.get("intent")).toBe(INTENT);
  });
});
