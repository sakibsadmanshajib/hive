/**
 * Issue #856: the console analytics page read zero in every tab and every
 * time window while the account's balance card correctly showed spend
 * happening. The control-plane handlers
 * (apps/control-plane/internal/usage/http.go) wrap their rows under
 * `{"usage": [...]}`, `{"spend": [...]}`, and `{"errors": [...]}`
 * respectively (see handleAnalyticsUsage/Spend/Errors), but every one of
 * getAnalyticsUsage/getAnalyticsSpend/getAnalyticsErrors in
 * lib/control-plane/client.ts looked for a `data` field that the backend
 * never sends. Since `data` was always undefined, `readArrayField` always
 * returned null, so every call silently parsed to an empty array: a 200 OK
 * response with real rows still rendered as all-zero, on every window,
 * every tab, independent of what usage_events actually held. That is the
 * "no failed HTTP requests, every counter reads zero" symptom from #856
 * exactly: no backend defect, no tenant-scope defect, no timezone defect,
 * just a wrapper-key mismatch between this file and the handlers it calls.
 *
 * These tests pin each function to the real wrapper key its own endpoint
 * sends, so a future edit that drifts the two apart again fails loudly here
 * instead of only in a live demo walk.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockGetUser = vi.fn();
const mockGetSession = vi.fn();

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: vi.fn(() => ({
    auth: { getUser: mockGetUser, getSession: mockGetSession },
  })),
}));

const BASE_URL = "http://control-plane.internal:8081";
const SESSION_TOKEN = "<ACCESS_TOKEN>";

let nextResponse: Response = new Response("", { status: 200 });

function fakeFetch(): Promise<Response> {
  return Promise.resolve(nextResponse);
}

describe("control-plane analytics calls (#856)", () => {
  const previousBaseUrl = process.env.CONTROL_PLANE_BASE_URL;

  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = BASE_URL;
    mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: SESSION_TOKEN } },
    });
    vi.stubGlobal("fetch", fakeFetch);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    // CodeRabbit (PR #908): CONTROL_PLANE_BASE_URL is process-global, so
    // leaving it set here would make any other suite that reads it
    // order-dependent on this one having run first.
    if (previousBaseUrl === undefined) {
      delete process.env.CONTROL_PLANE_BASE_URL;
    } else {
      process.env.CONTROL_PLANE_BASE_URL = previousBaseUrl;
    }
  });

  it("parses getAnalyticsUsage against the real {usage: [...]} wrapper", async () => {
    nextResponse = new Response(
      JSON.stringify({
        usage: [
          {
            group_key: "hive-auto",
            total_input_tokens: 120,
            total_output_tokens: 340,
            total_credits_spent: 18,
            request_count: 2,
          },
        ],
      }),
      { status: 200 },
    );
    const { getAnalyticsUsage } = await import("../lib/control-plane/client");

    const rows = await getAnalyticsUsage({ group_by: "model", window: "24h" });

    expect(rows).toHaveLength(1);
    expect(rows[0].group_key).toBe("hive-auto");
    expect(rows[0].request_count).toBe(2);
    expect(rows[0].total_credits_spent).toBe(18);
  });

  it("parses getAnalyticsSpend against the real {spend: [...]} wrapper", async () => {
    nextResponse = new Response(
      JSON.stringify({
        spend: [
          { group_key: "hive-auto", total_credits: 18, entry_count: 2 },
        ],
      }),
      { status: 200 },
    );
    const { getAnalyticsSpend } = await import("../lib/control-plane/client");

    const rows = await getAnalyticsSpend({ group_by: "model", window: "24h" });

    expect(rows).toHaveLength(1);
    expect(rows[0].total_credits).toBe(18);
    expect(rows[0].entry_count).toBe(2);
  });

  it("parses getAnalyticsErrors against the real {errors: [...]} wrapper", async () => {
    nextResponse = new Response(
      JSON.stringify({
        errors: [
          {
            group_key: "hive-auto",
            error_count: 0,
            total_requests: 2,
            error_rate: 0,
          },
        ],
      }),
      { status: 200 },
    );
    const { getAnalyticsErrors } = await import("../lib/control-plane/client");

    const rows = await getAnalyticsErrors({ group_by: "model", window: "24h" });

    expect(rows).toHaveLength(1);
    expect(rows[0].total_requests).toBe(2);
  });

  // The detectability gap this PR's own reviewer named: readArrayField
  // collapses "key absent" and "key genuinely empty" to the same result
  // unless a caller distinguishes them. A 200 whose shape has drifted (a
  // rename, a proxy remangling a body, a nested wrapper change) must not
  // parse to the same silent empty array #856 shipped as; it must throw, so
  // the page's existing "Unable to load analytics" state catches it instead
  // of a third all-zero read with no signal anything broke.
  it("throws rather than silently parsing to empty when the expected key is missing", async () => {
    nextResponse = new Response(JSON.stringify({ rows: [] }), { status: 200 });
    const { getAnalyticsUsage } = await import("../lib/control-plane/client");

    await expect(
      getAnalyticsUsage({ group_by: "model", window: "24h" }),
    ).rejects.toThrow(/"usage"/);
  });

  it("throws rather than silently parsing to empty when the expected key is wrong-typed", async () => {
    nextResponse = new Response(JSON.stringify({ spend: "not-an-array" }), { status: 200 });
    const { getAnalyticsSpend } = await import("../lib/control-plane/client");

    await expect(
      getAnalyticsSpend({ group_by: "model", window: "24h" }),
    ).rejects.toThrow(/"spend"/);
  });

  it("still returns an empty array for a genuinely quiet account (key present, empty)", async () => {
    nextResponse = new Response(JSON.stringify({ errors: [] }), { status: 200 });
    const { getAnalyticsErrors } = await import("../lib/control-plane/client");

    await expect(
      getAnalyticsErrors({ group_by: "model", window: "24h" }),
    ).resolves.toEqual([]);
  });
});

describe("getUsageEvents rejects the filter combination the backend 400s on", () => {
  it("refuses a window preset together with explicit from/to bounds", async () => {
    // parseListEventsFilter (apps/control-plane/internal/usage/http.go)
    // answers that combination with a bad request, which the analytics page
    // would render as an unavailable tile without ever saying the request
    // was malformed on this side.
    const { getUsageEvents } = await import("../lib/control-plane/client");

    await expect(
      getUsageEvents({
        limit: 100,
        window: "7d",
        from: "2026-08-01T00:00:00Z",
        to: "2026-08-08T00:00:00Z",
      }),
    ).rejects.toThrow(/mutually exclusive/);
  });
});
