import { describe, it, expect } from "vitest";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";

describe("Error response shape", () => {
  it("returns the OpenAI error envelope with correct Content-Type", async () => {
    const res = await fetch(`${BASE_URL}/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
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

  it("unknown endpoints return invalid_request_error type", async () => {
    const res = await fetch(`${BASE_URL}/nonexistent`, {
      method: "GET",
    });

    expect(res.status).toBe(404);

    const body = await res.json();
    expect(body.error.type).toBe("invalid_request_error");
    expect(body.error.code).toBe("unknown_endpoint");
  });
});
