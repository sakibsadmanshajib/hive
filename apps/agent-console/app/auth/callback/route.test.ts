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

describe("agent-console auth callback redirects", () => {
  beforeEach(() => {
    exchangeError = null;
  });

  it("sends a successful code exchange to the basePath-prefixed task console", async () => {
    const response = await GET(new NextRequest("http://localhost/auth/callback?code=abc"));
    expect(response.headers.get("location")).toBe("http://localhost/agent-workspace/tasks");
  });

  it("sends a failed code exchange to the basePath-prefixed sign-in page", async () => {
    exchangeError = { message: "invalid code" };
    const response = await GET(new NextRequest("http://localhost/auth/callback?code=abc"));
    expect(response.headers.get("location")).toBe(
      "http://localhost/agent-workspace/auth/sign-in"
    );
  });

  it("sends a request with no code to the basePath-prefixed sign-in page", async () => {
    const response = await GET(new NextRequest("http://localhost/auth/callback"));
    expect(response.headers.get("location")).toBe(
      "http://localhost/agent-workspace/auth/sign-in"
    );
  });
});
