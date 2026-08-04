/**
 * Issue #552: the per-key rate-limits page crashed for every key. The limits
 * read was the only control-plane call in the console that used a bare
 * relative path, and a Server Component has no origin, so Node's fetch
 * rejects the URL before any request leaves the process.
 *
 * These tests pin the read and the write to the same shape every other
 * control-plane call already has: an absolute URL built from the configured
 * base, the caller's own session bearer, and a typed ControlPlaneError whose
 * status the page can branch on without matching on message text.
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
const KEY_ID = "11111111-1111-4111-8111-111111111111";
const SESSION_TOKEN = "<ACCESS_TOKEN>";

interface RecordedCall {
  url: string;
  method: string;
  authorization: string | null;
  body: string | null;
}

const recorded: RecordedCall[] = [];
let nextResponse: Response = new Response("", { status: 200 });

function urlOf(input: RequestInfo | URL): string {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.toString();
  return input.url;
}

function bodyOf(init: RequestInit | undefined): string | null {
  const body = init?.body;
  return typeof body === "string" ? body : null;
}

function fakeFetch(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  recorded.push({
    url: urlOf(input),
    method: init?.method ?? "GET",
    authorization: new Headers(init?.headers).get("authorization"),
    body: bodyOf(init),
  });
  return Promise.resolve(nextResponse);
}

function limitsPayload(): string {
  return JSON.stringify({
    api_key_id: KEY_ID,
    rpm: 60,
    tpm: 4000,
    tier_overrides: { verified: { rpm: 30, tpm: 2000 } },
  });
}

describe("control-plane API-key limits calls", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    recorded.length = 0;
    process.env.CONTROL_PLANE_BASE_URL = BASE_URL;
    mockGetUser.mockResolvedValue({ data: { user: { id: "u1" } }, error: null });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: SESSION_TOKEN } },
    });
    vi.stubGlobal("fetch", fakeFetch);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("reads limits from an absolute URL with the caller's session", async () => {
    nextResponse = new Response(limitsPayload(), { status: 200 });
    const { getApiKeyLimits } = await import("../lib/control-plane/client");

    const limits = await getApiKeyLimits(KEY_ID);

    expect(limits.rpm).toBe(60);
    expect(limits.tier_overrides.verified?.tpm).toBe(2000);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].url).toBe(
      `${BASE_URL}/api/v1/accounts/current/api-keys/${KEY_ID}/limits`,
    );
    expect(recorded[0].authorization).toBe(`Bearer ${SESSION_TOKEN}`);
  });

  it("writes limits with PUT and the validated body", async () => {
    nextResponse = new Response(limitsPayload(), { status: 200 });
    const { updateApiKeyLimits } = await import("../lib/control-plane/client");

    await updateApiKeyLimits(KEY_ID, {
      rpm: 60,
      tpm: 4000,
      tier_overrides: { verified: { rpm: 30, tpm: 2000 } },
    });

    expect(recorded).toHaveLength(1);
    expect(recorded[0].method).toBe("PUT");
    expect(recorded[0].url).toBe(
      `${BASE_URL}/api/v1/accounts/current/api-keys/${KEY_ID}/limits`,
    );
    expect(recorded[0].body).toBe(
      JSON.stringify({
        rpm: 60,
        tpm: 4000,
        tier_overrides: { verified: { rpm: 30, tpm: 2000 } },
      }),
    );
  });

  it("rejects an out-of-range write before it reaches the control-plane", async () => {
    const { updateApiKeyLimits } = await import("../lib/control-plane/client");

    await expect(
      updateApiKeyLimits(KEY_ID, { rpm: -1, tpm: 0, tier_overrides: {} }),
    ).rejects.toThrow(/RPM/);
    expect(recorded).toHaveLength(0);
  });

  it("surfaces an unknown key as a 404 ControlPlaneError, not a message match", async () => {
    nextResponse = new Response(JSON.stringify({ error: "api key not found" }), {
      status: 404,
    });
    const { getApiKeyLimits, ControlPlaneError } = await import(
      "../lib/control-plane/client"
    );

    const failure = await getApiKeyLimits(KEY_ID).catch(
      (err: unknown): unknown => err,
    );

    expect(failure).toBeInstanceOf(ControlPlaneError);
    if (failure instanceof ControlPlaneError) {
      expect(failure.status).toBe(404);
    }
  });
});
