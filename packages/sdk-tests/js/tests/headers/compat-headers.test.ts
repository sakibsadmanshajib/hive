import { describe, it, expect } from "vitest";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const authHeaders = { Authorization: `Bearer ${API_KEY}` };

describe("Compatibility headers", () => {
  it("includes x-request-id, openai-version, and openai-processing-ms on success responses", async () => {
    const res = await fetch(`${BASE_URL}/models`, { headers: authHeaders });

    expect(res.status).toBe(200);

    const requestId = res.headers.get("x-request-id");
    expect(requestId).toBeTruthy();
    expect(typeof requestId).toBe("string");
    expect(requestId!.length).toBeGreaterThan(0);

    expect(res.headers.get("openai-version")).toBe("2020-10-01");

    const processingMs = res.headers.get("openai-processing-ms");
    expect(processingMs).toBeTruthy();
    expect(Number(processingMs)).toBeGreaterThanOrEqual(0);
  });

  it("includes compatibility headers on error responses too", async () => {
    const res = await fetch(`${BASE_URL}/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders },
      body: JSON.stringify({}),
    });

    expect(res.status).toBe(404);

    const requestId = res.headers.get("x-request-id");
    expect(requestId).toBeTruthy();
    expect(requestId!.length).toBeGreaterThan(0);

    expect(res.headers.get("openai-version")).toBe("2020-10-01");

    const processingMs = res.headers.get("openai-processing-ms");
    expect(processingMs).toBeTruthy();
    expect(Number(processingMs)).toBeGreaterThanOrEqual(0);
  });

  it("generates unique x-request-id per request", async () => {
    const res1 = await fetch(`${BASE_URL}/models`, { headers: authHeaders });
    const res2 = await fetch(`${BASE_URL}/models`, { headers: authHeaders });

    const id1 = res1.headers.get("x-request-id");
    const id2 = res2.headers.get("x-request-id");

    expect(id1).toBeTruthy();
    expect(id2).toBeTruthy();
    expect(id1).not.toBe(id2);
  });
});
