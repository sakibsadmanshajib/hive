import { describe, it, expect } from "vitest";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";

describe("Streaming error handling", () => {
  it("returns a JSON error (not a stream) for unsupported streaming endpoints", async () => {
    const res = await fetch(`${BASE_URL}/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: "gpt-4o",
        messages: [{ role: "user", content: "hello" }],
        stream: true,
      }),
    });

    expect(res.status).toBe(404);

    // Should NOT be a streaming response
    const contentType = res.headers.get("content-type");
    expect(contentType).toContain("application/json");
    expect(contentType).not.toContain("text/event-stream");

    const body = await res.json();

    expect(body).toHaveProperty("error");
    expect(body.error.type).toBe("unsupported_endpoint");
    expect(body.error.code).toBe("endpoint_not_available");
  });
});
