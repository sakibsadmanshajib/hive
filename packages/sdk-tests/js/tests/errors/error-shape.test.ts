import { describe, it, expect } from "vitest";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";

describe("Error response shape", () => {
  it("returns the OpenAI error envelope with correct Content-Type", async () => {
    const res = await fetch(`${BASE_URL}/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${API_KEY}`,
      },
      body: JSON.stringify({
        model: "gpt-4o",
        messages: [{ role: "user", content: "hello" }],
      }),
    });

    expect(res.status).toBe(404);
    expect(res.headers.get("content-type")).toContain("application/json");

    const body = await res.json();

    // Verify the exact envelope shape
    expect(body).toHaveProperty("error");
    expect(body.error).toHaveProperty("message");
    expect(body.error).toHaveProperty("type");
    expect(body.error).toHaveProperty("param");
    expect(body.error).toHaveProperty("code");

    expect(typeof body.error.message).toBe("string");
    expect(typeof body.error.type).toBe("string");
    expect(body.error.param).toBeNull();
    expect(typeof body.error.code).toBe("string");
  });

  // The Authorization header is load-bearing and was missing, which is why
  // this asserted 404 and measured 401 on the live run. Everything under
  // /v1/ passes through the auth selector before the mux sees it, so an
  // unauthenticated request is answered as unauthenticated no matter which
  // path it names. That ordering is not a defect and it is what the real
  // OpenAI API does too; issue #1377 covers the one route where it IS a
  // defect, /v1/audio/voices, which is deliberately registered without an
  // authorizer and never reached.
  //
  // With a credential present the request reaches the endpoint check and
  // answers what this test is actually about. Verified against the deployed
  // gateway: GET /v1/nonexistent with an hk_ bearer returns 404 with
  // {"code":"unknown_endpoint","type":"invalid_request_error"}. The key does
  // not need to be valid, because the unsupported-endpoint middleware sits
  // outside the mux and therefore ahead of any per-route authorizer.
  it("unknown endpoints return invalid_request_error type", async () => {
    const res = await fetch(`${BASE_URL}/nonexistent`, {
      method: "GET",
      headers: {
        Authorization: `Bearer ${API_KEY}`,
      },
    });

    expect(res.status).toBe(404);

    const body = await res.json();
    expect(body.error.type).toBe("invalid_request_error");
    expect(body.error.code).toBe("unknown_endpoint");
  });
});
