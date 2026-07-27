// @vitest-environment node
//
// Same runtime constraint as middleware.test.ts: route handlers run in Next's
// server runtime, where NextResponse rejects a request built with jsdom's
// Headers implementation.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { NextRequest } from "next/server";

// Regression guard for the basePath-less redirect: NextResponse.redirect takes
// an absolute URL and Next.js does not prepend basePath to it, so a target of
// new URL("/tasks", origin) escaped Caddy's /agent-workspace/* route and was
// answered by Open WebUI's catch-all.
//
// Second guard, same bug family: the *origin* half. Next.js builds request.url
// from the server's own bind address rather than the Host header, and this
// container starts with `--hostname 0.0.0.0`, so a target resolved against
// request.url was emitted as `https://0.0.0.0:3000/agent-workspace/...`. The
// forwarded-host cases below pin the origin the same way these first cases pin
// the path.
let exchangeError: { message: string } | null = null;

vi.mock("@supabase/ssr", () => ({
  createServerClient: () => ({
    auth: {
      exchangeCodeForSession: () => Promise.resolve({ error: exchangeError }),
    },
  }),
}));

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ getAll: () => [], set: () => {} }),
}));

import { GET } from "./route";

// Request's constructor does not synthesize a Host header, so these tests state
// it explicitly. That is what a real server sees, and it keeps the path
// assertions below reading against the same `http://localhost` origin they
// always did.
function request(path: string, headers: Record<string, string> = {}): NextRequest {
  return new NextRequest(`http://localhost${path}`, {
    headers: { host: "localhost", ...headers },
  });
}

// The shape the origin bug actually takes in production: the request URL sits
// on the wildcard bind address while Caddy and Cloudflare Tunnel supply the
// real host and scheme.
function forwardedRequest(path: string): NextRequest {
  return new NextRequest(`http://0.0.0.0:3000${path}`, {
    headers: {
      host: "0.0.0.0:3000",
      "x-forwarded-host": "chat-hive.scubed.co",
      "x-forwarded-proto": "https",
    },
  });
}

describe("agent-console auth callback redirects", () => {
  beforeEach(() => {
    exchangeError = null;
    delete process.env.NEXT_PUBLIC_APP_URL;
  });

  it("sends a successful code exchange to the basePath-prefixed task console", async () => {
    const response = await GET(request("/auth/callback?code=abc"));
    expect(response.headers.get("location")).toBe("http://localhost/agent-workspace/tasks");
  });

  it("sends a failed code exchange to the basePath-prefixed sign-in page", async () => {
    exchangeError = { message: "invalid code" };
    const response = await GET(request("/auth/callback?code=abc"));
    expect(response.headers.get("location")).toBe(
      "http://localhost/agent-workspace/auth/sign-in"
    );
  });

  it("sends a request with no code to the basePath-prefixed sign-in page", async () => {
    const response = await GET(request("/auth/callback"));
    expect(response.headers.get("location")).toBe(
      "http://localhost/agent-workspace/auth/sign-in"
    );
  });

  it("resolves a successful exchange against the forwarded host, not the wildcard bind address", async () => {
    const response = await GET(forwardedRequest("/auth/callback?code=abc"));
    expect(response.headers.get("location")).toBe(
      "https://chat-hive.scubed.co/agent-workspace/tasks"
    );
  });

  it("resolves a failed exchange against the forwarded host, not the wildcard bind address", async () => {
    exchangeError = { message: "invalid code" };
    const response = await GET(forwardedRequest("/auth/callback?code=abc"));
    expect(response.headers.get("location")).toBe(
      "https://chat-hive.scubed.co/agent-workspace/auth/sign-in"
    );
  });

  it("never emits the wildcard bind address on any branch", async () => {
    exchangeError = { message: "invalid code" };
    const response = await GET(forwardedRequest("/auth/callback?code=abc"));
    expect(response.headers.get("location")).not.toContain("0.0.0.0");
  });

  it("keeps the basePath prefix while resolving against the forwarded host", async () => {
    // Both halves of this bug family at once: the path must stay inside
    // Caddy's /agent-workspace/* route and the origin must be the real host.
    const response = await GET(forwardedRequest("/auth/callback?code=abc"));
    const location = new URL(response.headers.get("location") ?? "");
    expect(location.host).toBe("chat-hive.scubed.co");
    expect(location.pathname.startsWith("/agent-workspace/")).toBe(true);
  });
});
