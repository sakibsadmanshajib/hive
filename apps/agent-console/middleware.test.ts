// @vitest-environment node
//
// Middleware runs in Next's server/edge runtime, not a browser DOM. jsdom
// (this project's default test environment) supplies its own Headers
// class, and Next's NextResponse.next() rejects a request whose .headers
// isn't Node's own Headers instance ("request.headers must be an instance
// of Headers") -- the per-file override below switches just this file.
import { describe, it, expect, vi, afterEach } from "vitest";
import { NextRequest } from "next/server";

// Real Next.js server behavior, verified live against the built production
// image (docker build + curl), not assumed: basePath is stripped from
// request.nextUrl.pathname before middleware runs, so a request that
// actually arrived at /agent-workspace/tasks is seen here as pathname
// "/tasks". That's what these NextRequest URLs (no basePath segment)
// simulate. See middleware.ts's BASE_PATH comment for the redirect-target
// half of this same finding.
let mockUser: { id: string } | null = null;

vi.mock("@supabase/ssr", () => ({
  createServerClient: () => ({
    auth: {
      getUser: () => Promise.resolve({ data: { user: mockUser } }),
    },
  }),
}));

import { middleware } from "./middleware";

describe("middleware basePath-aware redirects", () => {
  afterEach(() => {
    mockUser = null;
    vi.restoreAllMocks();
  });

  it("redirects unauthenticated /tasks to the basePath-prefixed sign-in URL", async () => {
    mockUser = null;
    const request = new NextRequest("http://localhost/tasks");
    const response = await middleware(request);
    expect(response.headers.get("location")).toBe("http://localhost/agent-workspace/auth/sign-in");
  });

  it("passes through authenticated /tasks with no redirect", async () => {
    mockUser = { id: "user-1" };
    const request = new NextRequest("http://localhost/tasks");
    const response = await middleware(request);
    expect(response.headers.get("location")).toBeNull();
  });

  it("redirects root to the basePath-prefixed /tasks when authenticated", async () => {
    mockUser = { id: "user-1" };
    const request = new NextRequest("http://localhost/");
    const response = await middleware(request);
    expect(response.headers.get("location")).toBe("http://localhost/agent-workspace/tasks");
  });

  it("redirects root to the basePath-prefixed sign-in URL when unauthenticated", async () => {
    mockUser = null;
    const request = new NextRequest("http://localhost/");
    const response = await middleware(request);
    expect(response.headers.get("location")).toBe("http://localhost/agent-workspace/auth/sign-in");
  });

  it("allows same-origin framing and refuses every other origin", async () => {
    // The chat shell renders this app as its Agents destination, on the same
    // origin behind the same Caddy listener, so 'none' is no longer correct.
    // 'self' is still a real origin check: a third-party page cannot frame this
    // one to harvest a click or a session.
    mockUser = { id: "user-1" };
    const request = new NextRequest("http://localhost/tasks");
    const response = await middleware(request);
    expect(response.headers.get("x-frame-options")).toBe("SAMEORIGIN");
    expect(response.headers.get("content-security-policy")).toBe(
      "frame-ancestors 'self'"
    );
  });

  // Regression guard for a bug that no route-handler test can see. Middleware
  // headers do not merge with a handler's: this `set` replaces one the deck
  // proxy already wrote, so before the exemption the handler's
  // `sandbox allow-scripts` was silently deleted in production while every
  // unit test calling the handler directly stayed green. Verified by curl
  // against the built image, both before and after.
  it("leaves the deck proxy's own security headers alone", async () => {
    mockUser = { id: "user-1" };
    const request = new NextRequest("http://localhost/api/deck/abc-123");
    const response = await middleware(request);
    expect(response.headers.get("content-security-policy")).toBeNull();
    expect(response.headers.get("x-frame-options")).toBeNull();
  });

  it("still stamps its own security headers on every other route", async () => {
    // The exemption is one path prefix, not a hole: a route that merely looks
    // adjacent keeps the blanket policy.
    mockUser = { id: "user-1" };
    const request = new NextRequest("http://localhost/api/deckle");
    const response = await middleware(request);
    expect(response.headers.get("content-security-policy")).toBe(
      "frame-ancestors 'self'"
    );
  });

  it("carries the embed and theme parameters through the sign-in redirect", async () => {
    // Without this the panel loses the shell's theme the moment it bounces to
    // sign-in, and a light login appears inside a dark shell.
    mockUser = null;
    const request = new NextRequest(
      "http://localhost/tasks?embed=1&theme=dark"
    );
    const response = await middleware(request);
    expect(response.headers.get("location")).toBe(
      "http://localhost/agent-workspace/auth/sign-in?embed=1&theme=dark"
    );
  });

  it("ignores a theme it does not know", async () => {
    mockUser = null;
    const request = new NextRequest(
      "http://localhost/tasks?embed=1&theme=neon"
    );
    const response = await middleware(request);
    expect(response.headers.get("location")).toBe(
      "http://localhost/agent-workspace/auth/sign-in?embed=1"
    );
  });
});
