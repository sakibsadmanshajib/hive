/**
 * TDD: signup-precheck proxy route (issue #116).
 *
 * Verifies that the Route Handler at /api/auth/signup-precheck:
 * 1. Forwards { email, captcha_token } to the control-plane precheck endpoint.
 * 2. Returns the upstream status and body verbatim on success (200).
 * 3. Returns the upstream status and body verbatim on rejection (403/429).
 * 4. Returns 503 when CONTROL_PLANE_BASE_URL is not set.
 * 5. Returns 400 on malformed request body.
 * 6. Returns 503 on network failure (fetch throws).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

describe("app/api/auth/signup-precheck/route.ts", () => {
  const originalEnv = process.env.CONTROL_PLANE_BASE_URL;

  beforeEach(() => {
    vi.resetModules();
    vi.unstubAllGlobals();
  });

  afterEach(() => {
    if (originalEnv === undefined) {
      delete process.env.CONTROL_PLANE_BASE_URL;
    } else {
      process.env.CONTROL_PLANE_BASE_URL = originalEnv;
    }
    vi.unstubAllGlobals();
  });

  it("forwards the request to the control-plane precheck endpoint and returns 200", async () => {
    process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ status: "ok" }), { status: 200 })
      );
    vi.stubGlobal("fetch", fetchMock);

    const { POST } = await import(
      "../app/api/auth/signup-precheck/route"
    );
    const req = new Request("http://localhost:3000/api/auth/signup-precheck", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: "user@example.com", captcha_token: "tok123" }),
    });

    const res = await POST(req);
    expect(res.status).toBe(200);

    const [calledUrl, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(calledUrl).toBe(
      "http://localhost:8081/api/v1/auth/sign-up/precheck"
    );
    expect(init.method).toBe("POST");
    const sentBody = JSON.parse(init.body as string) as {
      email: string;
      captcha_token: string;
    };
    expect(sentBody.email).toBe("user@example.com");
    expect(sentBody.captcha_token).toBe("tok123");
  });

  it("returns 403 when the control-plane rejects the request", async () => {
    process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({
            error:
              "We could not complete your sign-up. Please try again or use a different email address.",
          }),
          { status: 403 }
        )
      );
    vi.stubGlobal("fetch", fetchMock);

    const { POST } = await import(
      "../app/api/auth/signup-precheck/route"
    );
    const req = new Request("http://localhost:3000/api/auth/signup-precheck", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: "throwaway@mailnull.com", captcha_token: "" }),
    });

    const res = await POST(req);
    expect(res.status).toBe(403);
  });

  it("returns 503 when CONTROL_PLANE_BASE_URL is not configured", async () => {
    delete process.env.CONTROL_PLANE_BASE_URL;

    const { POST } = await import(
      "../app/api/auth/signup-precheck/route"
    );
    const req = new Request("http://localhost:3000/api/auth/signup-precheck", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: "user@example.com", captcha_token: "" }),
    });

    const res = await POST(req);
    expect(res.status).toBe(503);
  });

  it("returns 400 on malformed request body", async () => {
    process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";

    const { POST } = await import(
      "../app/api/auth/signup-precheck/route"
    );
    const req = new Request("http://localhost:3000/api/auth/signup-precheck", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "not-json",
    });

    const res = await POST(req);
    expect(res.status).toBe(400);
  });

  it("returns 503 when fetch throws (network failure)", async () => {
    process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("ECONNREFUSED")));

    const { POST } = await import(
      "../app/api/auth/signup-precheck/route"
    );
    const req = new Request("http://localhost:3000/api/auth/signup-precheck", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: "user@example.com", captcha_token: "" }),
    });

    const res = await POST(req);
    expect(res.status).toBe(503);
  });
});
