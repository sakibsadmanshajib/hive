// @vitest-environment node
//
// Node environment (not jsdom) because NextResponse rejects a request built
// with jsdom's Headers implementation, the same reason redirect-origin.test.ts
// gives.
//
// Issue #1457. The middleware is where the cross-origin refusal actually
// applies to every state-changing request, including routes nobody remembers to
// guard by hand. These tests pin that, pin the two deliberate exemptions (safe
// methods, and the SSLCommerz payment return), and pin that the matcher still
// covers every mutating route in the app, since a narrowed matcher is the one
// edit that would remove the protection silently.
import { readdirSync } from "node:fs";
import { join } from "node:path";

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";

const CANONICAL = "https://console.example.test";

const mockGetUser = vi.fn();
const mockCreateServerClient = vi.fn(() => ({ auth: { getUser: mockGetUser } }));

vi.mock("@supabase/ssr", () => ({
  createServerClient: mockCreateServerClient,
}));

function request(
  method: string,
  path: string,
  headers: Record<string, string>,
): NextRequest {
  // The wildcard bind address is what Next actually composes a request URL
  // from; the forwarded headers are what Caddy sets. Both matter here, because
  // the guard compares against the forwarded host as well as the configured
  // origin.
  return new NextRequest(`http://0.0.0.0:3000${path}`, { method, headers });
}

describe("middleware cross-origin refusal (issue #1457)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubEnv("NEXT_PUBLIC_APP_URL", CANONICAL);
    vi.stubEnv("NEXT_PUBLIC_SUPABASE_URL", "https://supabase.example.test");
    vi.stubEnv("NEXT_PUBLIC_SUPABASE_ANON_KEY", "anon-key");
    mockGetUser.mockResolvedValue({ data: { user: null }, error: null });
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("refuses a cross-origin POST with 403 and never opens a session", async () => {
    const { middleware } = await import("../middleware");
    const res = await middleware(
      request("POST", "/api/console/members", { origin: "https://evil.example" }),
    );

    expect(res.status).toBe(403);
    const body: unknown = await res.json();
    expect(body).toEqual({ error: "Forbidden" });
    // The refusal has to land before the GoTrue round trip, not after it.
    expect(mockCreateServerClient).not.toHaveBeenCalled();
    expect(mockGetUser).not.toHaveBeenCalled();
  });

  it("keeps the anti-framing headers on the refusal", async () => {
    const { middleware } = await import("../middleware");
    const res = await middleware(
      request("POST", "/api/console/members", { origin: "https://evil.example" }),
    );

    // The status assertion is not redundant: without it this case would pass on
    // the ordinary response too, since every response from this middleware
    // carries the same two headers.
    expect(res.status).toBe(403);
    expect(res.headers.get("X-Frame-Options")).toBe("DENY");
    expect(res.headers.get("Content-Security-Policy")).toBe("frame-ancestors 'none'");
  });

  // The routes the issue did not name, which are the more valuable targets: an
  // API key mint, a spend-alert webhook, the active-workspace cookie and
  // sign-out. None of them carries a guard of its own; the middleware is what
  // covers them.
  it.each([
    "/api/v1/accounts/current/api-keys",
    "/api/spend-alerts/00000000-0000-0000-0000-000000000000",
    "/api/budget",
    "/console/account-switch",
    "/auth/sign-out",
  ])("refuses a cross-origin POST to %s", async (path) => {
    const { middleware } = await import("../middleware");
    const res = await middleware(
      request("POST", path, { origin: "https://artifacts-hive.scubed.co" }),
    );

    expect(res.status).toBe(403);
  });

  it("lets a same-origin POST through", async () => {
    const { middleware } = await import("../middleware");
    const res = await middleware(
      request("POST", "/api/console/members", { origin: CANONICAL }),
    );

    expect(res.status).not.toBe(403);
    expect(mockGetUser).toHaveBeenCalled();
  });

  it("lets a POST whose Origin matches the forwarded host through", async () => {
    vi.stubEnv("NEXT_PUBLIC_APP_URL", "https://console-hive.scubed.co");
    const { middleware } = await import("../middleware");
    const res = await middleware(
      request("POST", "/api/console/members", {
        origin: "http://console.localhost",
        "x-forwarded-host": "console.localhost",
        "x-forwarded-proto": "http",
      }),
    );

    expect(res.status).not.toBe(403);
  });

  it("lets a cross-origin GET through", async () => {
    const { middleware } = await import("../middleware");
    const res = await middleware(
      request("GET", "/console", { origin: "https://evil.example" }),
    );

    expect(res.status).not.toBe(403);
  });

  it("lets the SSLCommerz return POST through", async () => {
    const { middleware } = await import("../middleware");
    const res = await middleware(
      request("POST", "/api/payments/return/sslcommerz", {
        origin: "https://sandbox.sslcommerz.com",
      }),
    );

    expect(res.status).not.toBe(403);
  });
});

// The guard lives in the middleware, so the matcher is the thing that decides
// what it covers. A later edit that narrows it (excluding /api, say) would take
// the protection off every route silently. This walks the route tree and
// asserts the matcher still selects each mutating route's own path.
describe("the middleware matcher still covers every mutating route", () => {
  const appDir =
    process.cwd().endsWith("apps/web-console")
      ? join(process.cwd(), "app")
      : join(process.cwd(), "apps/web-console/app");

  function routeFiles(dir: string): string[] {
    const found: string[] = [];
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) found.push(...routeFiles(path));
      else if (entry.name === "route.ts") found.push(path);
    }
    return found;
  }

  // app/api/console/marketplace/[id]/route.ts -> /api/console/marketplace/x
  function urlPathOf(file: string): string {
    const relative = file.slice(appDir.length).replace(/\/route\.ts$/, "");
    return relative
      .split("/")
      .map((segment) =>
        segment.startsWith("[") ? (segment.startsWith("[...") ? "a/b" : "x") : segment,
      )
      .join("/");
  }

  const mutating = /export\s+(?:async\s+)?(?:function|const|let|var)\s+(?:POST|PUT|PATCH|DELETE)\b/;

  it("selects the path of every route file that exports a mutating handler", async () => {
    const { config } = await import("../middleware");
    const { readFileSync } = await import("node:fs");

    // The matcher is written as a regular expression rather than a
    // path-to-regexp pattern, so it can be evaluated directly here.
    const pattern = new RegExp(`^${config.matcher[0]}$`);

    const paths = routeFiles(appDir)
      .filter((file) => mutating.test(readFileSync(file, "utf8")))
      .map(urlPathOf);

    // A walk that found nothing would make the loop below vacuous.
    expect(paths.length).toBeGreaterThanOrEqual(15);

    const uncovered = paths.filter((path) => !pattern.test(path));
    expect(uncovered).toEqual([]);
  });
});
