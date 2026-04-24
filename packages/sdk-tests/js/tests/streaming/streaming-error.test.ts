import { describe, it, expect } from "vitest";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";

describe("Streaming error handling", () => {
  it("returns a JSON error (not a stream) for unsupported endpoints", async () => {
    // GET /v1/models/{model} is planned_for_launch — the middleware
    // returns a structured unsupported_endpoint JSON error, not an SSE
    // stream, regardless of Accept negotiation.
    const res = await fetch(`${BASE_URL}/models/hive-default`, {
      method: "GET",
      headers: {
        Accept: "text/event-stream",
        Authorization: `Bearer ${API_KEY}`,
      },
    });

    expect(res.status).toBe(404);

    const contentType = res.headers.get("content-type");
    expect(contentType).toContain("application/json");
    expect(contentType).not.toContain("text/event-stream");

    const body = await res.json();

    expect(body).toHaveProperty("error");
    expect(body.error.type).toBe("unsupported_endpoint");
    expect(body.error.code).toBe("endpoint_not_available");
  });
});
