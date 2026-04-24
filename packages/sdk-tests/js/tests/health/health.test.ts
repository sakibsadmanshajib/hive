import { describe, it, expect } from "vitest";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";

/** Derive the root URL by stripping the /v1 suffix. */
function rootURL(): string {
  return BASE_URL.replace(/\/v1\/?$/, "");
}

describe("Health endpoint", () => {
  it("returns 200 with status ok", async () => {
    const res = await fetch(`${rootURL()}/health`);

    expect(res.status).toBe(200);

    const body = await res.json();
    expect(body).toEqual({ status: "ok" });
  });
});
