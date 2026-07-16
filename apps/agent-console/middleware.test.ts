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

  it("sets frame-denial headers on every response", async () => {
    mockUser = { id: "user-1" };
    const request = new NextRequest("http://localhost/tasks");
    const response = await middleware(request);
    expect(response.headers.get("x-frame-options")).toBe("DENY");
  });
});
