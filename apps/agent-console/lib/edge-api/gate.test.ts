import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { isCoworkEnabled } from "./gate";

let mockSession: { access_token: string } | null = { access_token: "test-token" };

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ getAll: () => [], set: () => {} }),
}));

vi.mock("@/lib/supabase/server", () => ({
  createClient: () => ({
    auth: {
      getSession: () => Promise.resolve({ data: { session: mockSession } }),
    },
  }),
}));

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("isCoworkEnabled", () => {
  const originalBaseUrl = process.env.EDGE_API_INTERNAL_BASE_URL;

  beforeEach(() => {
    mockSession = { access_token: "test-token" };
    process.env.EDGE_API_INTERNAL_BASE_URL = "http://edge-api.test";
  });

  afterEach(() => {
    vi.restoreAllMocks();
    process.env.EDGE_API_INTERNAL_BASE_URL = originalBaseUrl;
  });

  it("returns true when the tenant's ENABLE_COWORK gate is on", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ gates: { ENABLE_COWORK: true } })),
    );
    expect(await isCoworkEnabled()).toBe(true);
  });

  it("returns false when the tenant's ENABLE_COWORK gate is off", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ gates: { ENABLE_COWORK: false } })),
    );
    expect(await isCoworkEnabled()).toBe(false);
  });

  it("fails closed when there is no active session", async () => {
    mockSession = null;
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    expect(await isCoworkEnabled()).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("fails closed when EDGE_API_INTERNAL_BASE_URL is not configured", async () => {
    delete process.env.EDGE_API_INTERNAL_BASE_URL;
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    expect(await isCoworkEnabled()).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("fails closed on a non-2xx response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("nope", { status: 500 })));
    expect(await isCoworkEnabled()).toBe(false);
  });

  it("fails closed on a network error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    expect(await isCoworkEnabled()).toBe(false);
  });

  it("fails closed on a malformed gates payload", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ gates: "not-an-object" })));
    expect(await isCoworkEnabled()).toBe(false);
  });
});
