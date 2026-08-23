import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { GET } from "./route";

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

const DECK_HTML = "<!doctype html><title>Deck</title><script>next()</script>";

function htmlResponse(status = 200): Response {
  return new Response(DECK_HTML, {
    status,
    headers: { "Content-Type": "text/html; charset=utf-8" },
  });
}

/** What the route asked edge-api for, recorded rather than re-derived. */
interface UpstreamCall {
  url: string;
  headers: Headers;
}

/**
 * Records every upstream fetch. Typed through the real fetch signature so the
 * assertions below read structurally valid values, with no casts.
 */
function stubFetch(respond: () => Promise<Response>): UpstreamCall[] {
  const calls: UpstreamCall[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      calls.push({ url: String(input), headers: new Headers(init?.headers) });
      return respond();
    }),
  );
  return calls;
}

const servesDeck = () => Promise.resolve(htmlResponse());

/**
 * The handler only ever reads `params`, never the request body or URL, but
 * Next hands it a real Request and the signature says so. Building one keeps
 * the call honest rather than passing a stand-in the route cannot receive.
 */
function call(segments: string[]): Promise<Response> {
  const path = segments.map(encodeURIComponent).join("/");
  return GET(new Request(`https://console.test/agent-workspace/api/deck/${path}`), {
    params: Promise.resolve({ ref: segments }),
  });
}

describe("GET /agent-workspace/api/deck/[...ref]", () => {
  const originalBaseUrl = process.env.EDGE_API_INTERNAL_BASE_URL;

  beforeEach(() => {
    mockSession = { access_token: "test-token" };
    process.env.EDGE_API_INTERNAL_BASE_URL = "http://edge-api.test";
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    process.env.EDGE_API_INTERNAL_BASE_URL = originalBaseUrl;
  });

  it("serves the deck to the signed-in owner", async () => {
    const calls = stubFetch(servesDeck);

    const response = await call(["abc-123"]);

    expect(response.status).toBe(200);
    expect(await response.text()).toBe(DECK_HTML);
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("http://edge-api.test/artifacts/abc-123");
  });

  it("forwards the caller's own access token upstream", async () => {
    const calls = stubFetch(servesDeck);

    await call(["abc-123"]);

    expect(calls[0].headers.get("Authorization")).toBe("Bearer test-token");
  });

  it("serves the versioned form", async () => {
    const calls = stubFetch(servesDeck);

    const response = await call(["abc-123", "v", "2"]);

    expect(response.status).toBe(200);
    expect(calls[0].url).toBe("http://edge-api.test/artifacts/abc-123/v/2");
  });

  // GUARD 1: no session.
  it("returns 401 with no session, and never calls upstream", async () => {
    mockSession = null;
    const calls = stubFetch(servesDeck);

    const response = await call(["abc-123"]);

    expect(response.status).toBe(401);
    expect(calls).toHaveLength(0);
  });

  it("returns 401 when the session carries no access token", async () => {
    mockSession = { access_token: "" };
    const calls = stubFetch(servesDeck);

    const response = await call(["abc-123"]);

    expect(response.status).toBe(401);
    expect(calls).toHaveLength(0);
  });

  // GUARD 2: ref shape. Each of these must be rejected before any upstream
  // call: a path-traversal segment, an extra path segment, and a bad version
  // form.
  it.each([
    { name: "path traversal", segments: ["..", "..", "etc", "passwd"] },
    { name: "traversal inside one decoded segment", segments: ["../../etc/passwd"] },
    { name: "an extra trailing segment", segments: ["abc-123", "raw"] },
    { name: "a non-numeric version", segments: ["abc-123", "v", "latest"] },
    { name: "a non-v two-part suffix", segments: ["abc-123", "x", "2"] },
    { name: "an empty id", segments: [""] },
    { name: "no id at all", segments: [] },
    { name: "a dot segment", segments: ["."] },
    { name: "a dot-dot segment that would normalize away upstream", segments: [".."] },
  ])("returns 400 for $name, and never calls upstream", async ({ segments }) => {
    const calls = stubFetch(servesDeck);

    const response = await call(segments);

    expect(response.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  it("rejects an id carrying a query-string smuggle rather than encoding it upstream", async () => {
    const calls = stubFetch(servesDeck);

    const response = await call(["abc?injected=1"]);

    expect(response.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  // GUARD 3: the response is origin-isolated.
  it("emits a CSP that sandboxes the deck into an opaque origin", async () => {
    stubFetch(servesDeck);

    const response = await call(["abc-123"]);
    const csp = response.headers.get("Content-Security-Policy") ?? "";

    // allow-scripts with no allow-same-origin: the deck's own navigation JS
    // runs, but the document cannot reach this app's origin or its cookies.
    expect(csp).toContain("sandbox allow-scripts");
    expect(csp).not.toContain("allow-same-origin");
  });

  it("mirrors edge-api's strict artifact CSP directives", async () => {
    stubFetch(servesDeck);

    const response = await call(["abc-123"]);
    const csp = response.headers.get("Content-Security-Policy") ?? "";

    for (const directive of [
      "default-src 'none'",
      "script-src 'unsafe-inline'",
      "style-src 'unsafe-inline'",
      "img-src data:",
      "connect-src 'none'",
      "base-uri 'none'",
      "form-action 'none'",
      "frame-ancestors 'none'",
    ]) {
      expect(csp).toContain(directive);
    }
  });

  // middleware.ts exempts this path from the blanket CSP it stamps elsewhere
  // (that `set` would replace the sandbox directive rather than merge with
  // it), so an error exit that carried no policy would carry none at all.
  it("carries the same policy on an error exit", async () => {
    mockSession = null;
    stubFetch(servesDeck);

    const response = await call(["abc-123"]);

    expect(response.status).toBe(401);
    expect(response.headers.get("Content-Security-Policy")).toContain(
      "sandbox allow-scripts",
    );
  });

  it("sets nosniff, an explicit html content type, and no shared caching", async () => {
    stubFetch(servesDeck);

    const response = await call(["abc-123"]);

    expect(response.headers.get("X-Content-Type-Options")).toBe("nosniff");
    expect(response.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    expect(response.headers.get("Cache-Control")).toBe("private, no-store");
  });

  // GUARD 4: an upstream refusal never becomes a 200.
  it.each([401, 403, 404])(
    "collapses upstream %i to 404 rather than leaking existence",
    async (status) => {
      stubFetch(() => Promise.resolve(new Response("upstream detail", { status })));

      const response = await call(["abc-123"]);

      expect(response.status).toBe(404);
      expect(await response.text()).not.toContain("upstream detail");
    },
  );

  it("returns 502 on an upstream server error", async () => {
    stubFetch(() => Promise.resolve(new Response("boom", { status: 500 })));

    const response = await call(["abc-123"]);

    expect(response.status).toBe(502);
  });

  it("returns 502 on a network error", async () => {
    stubFetch(() => Promise.reject(new Error("network down")));

    const response = await call(["abc-123"]);

    expect(response.status).toBe(502);
  });

  it("returns 502 when EDGE_API_INTERNAL_BASE_URL is not configured", async () => {
    delete process.env.EDGE_API_INTERNAL_BASE_URL;
    const calls = stubFetch(servesDeck);

    const response = await call(["abc-123"]);

    expect(response.status).toBe(502);
    expect(calls).toHaveLength(0);
  });
});
