/**
 * TDD: Auth route and middleware contracts.
 *
 * These tests verify:
 * 1. middleware.ts exports a default function and proper config
 * 2. callback/route.ts calls exchangeCodeForSession
 * 3. callback/route.ts only allows /console and /auth/reset-password as next targets
 * 4. Sign-in page exports a default component
 * 5. Sign-up page exports a default component
 * 6. Forgot-password page exports a default component
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

// --- mock next/server for middleware tests ---
const mockRedirect = vi.fn((url: string) => ({ type: "redirect", url }));
const mockNext = vi.fn(() => ({ type: "next" }));

vi.mock("next/server", () => ({
  NextResponse: {
    redirect: mockRedirect,
    next: mockNext,
  },
  NextRequest: class {
    url: string;
    nextUrl: { pathname: string; searchParams: URLSearchParams };
    constructor(url: string) {
      this.url = url;
      const parsed = new URL(url);
      this.nextUrl = {
        pathname: parsed.pathname,
        searchParams: parsed.searchParams,
      };
    }
  },
}));

// --- mock @supabase/ssr for server client ---
const mockGetUser = vi.fn();
const mockExchangeCodeForSession = vi.fn();

vi.mock("@supabase/ssr", () => ({
  createServerClient: vi.fn(() => ({
    auth: {
      getUser: mockGetUser,
      exchangeCodeForSession: mockExchangeCodeForSession,
    },
  })),
  createBrowserClient: vi.fn(() => ({
    auth: { signInWithPassword: vi.fn(), signUp: vi.fn() },
  })),
}));

// --- mock next/headers ---
vi.mock("next/headers", () => ({
  cookies: vi.fn(() => ({
    getAll: vi.fn(() => []),
    set: vi.fn(),
  })),
}));

// --- mock next/navigation ---
vi.mock("next/navigation", () => ({
  redirect: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  process.env.NEXT_PUBLIC_SUPABASE_URL = "https://test.supabase.co";
  process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY = "anon-key-test";
  process.env.NEXT_PUBLIC_APP_URL = "http://localhost:3000";
});

describe("middleware.ts", () => {
  it("exports a middleware function (named export per Next.js convention)", async () => {
    const mod = await import("../middleware");
    expect(typeof mod.middleware).toBe("function");
  });

  it("exports a config with matcher", async () => {
    const mod = await import("../middleware");
    expect(mod.config).toBeDefined();
    expect(mod.config.matcher).toBeDefined();
  });
});

describe("app/auth/callback/route.ts", () => {
  it("exports a GET handler", async () => {
    const mod = await import("../app/auth/callback/route");
    expect(typeof mod.GET).toBe("function");
  });

  it("calls exchangeCodeForSession when code is present", async () => {
    const { NextRequest } = await import("next/server");
    const { createServerClient } = await import("@supabase/ssr");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req: Parameters<typeof mod.GET>[0] = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc123"
    );
    await mod.GET(req);

    expect(createServerClient).toHaveBeenCalled();
    expect(mockExchangeCodeForSession).toHaveBeenCalledWith("abc123");
  });

  it("redirects to /console when next param is missing", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req: Parameters<typeof mod.GET>[0] = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc"
    );
    await mod.GET(req);

    expect(mockRedirect).toHaveBeenCalledWith(
      expect.objectContaining({ href: expect.stringContaining("/console") })
    );
  });

  it("allows /auth/reset-password as a valid next target", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req: Parameters<typeof mod.GET>[0] = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc&next=/auth/reset-password"
    );
    await mod.GET(req);

    expect(mockRedirect).toHaveBeenCalledWith(
      expect.objectContaining({ href: expect.stringContaining("/auth/reset-password") })
    );
  });

  it("allows /console/settings/profile as a valid next target", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req: Parameters<typeof mod.GET>[0] = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc&next=/console/settings/profile"
    );
    await mod.GET(req);

    expect(mockRedirect).toHaveBeenCalledWith(
      expect.objectContaining({ href: expect.stringContaining("/console/settings/profile") })
    );
  });

  it("rejects arbitrary next targets and falls back to /console", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req: Parameters<typeof mod.GET>[0] = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc&next=/evil-redirect"
    );
    await mod.GET(req);

    expect(mockRedirect).toHaveBeenCalledWith(
      expect.objectContaining({ href: expect.stringContaining("/console") })
    );
  });

  it("finalizes email verification via the control-plane with the session bearer (#112)", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({
      error: null,
      data: { session: { access_token: "sess-token-xyz" } },
    });
    process.env.CONTROL_PLANE_BASE_URL = "http://control-plane:8081";
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200 });
    vi.stubGlobal("fetch", fetchMock);

    const mod = await import("../app/auth/callback/route");
    const req: Parameters<typeof mod.GET>[0] = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc&hive_verify=1"
    );
    await mod.GET(req);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [calledUrl, init] = fetchMock.mock.calls[0];
    expect(String(calledUrl)).toContain(
      "/api/v1/accounts/current/email-verification/finalize"
    );
    expect(init.method).toBe("POST");
    expect(init.headers.Authorization).toBe("Bearer sess-token-xyz");
    vi.unstubAllGlobals();
  });

  it("never calls the Supabase admin API or uses a service-role key (#112)", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({
      error: null,
      data: { session: { access_token: "sess-token-xyz" } },
    });
    process.env.CONTROL_PLANE_BASE_URL = "http://control-plane:8081";
    // If the route still reached for the service-role admin write, the URL
    // would contain /auth/v1/admin/users — assert it never does.
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200 });
    vi.stubGlobal("fetch", fetchMock);

    const mod = await import("../app/auth/callback/route");
    const req: Parameters<typeof mod.GET>[0] = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc&hive_verify=1"
    );
    await mod.GET(req);

    for (const [url, init] of fetchMock.mock.calls) {
      expect(String(url)).not.toContain("/auth/v1/admin/users");
      const auth = (init?.headers?.Authorization as string) ?? "";
      // The forwarded credential is the short user session token, never the
      // long service-role key.
      expect(auth).toBe("Bearer sess-token-xyz");
    }
    vi.unstubAllGlobals();
  });
});

describe("app/auth/sign-in/page.tsx", () => {
  it("exports a default React component", async () => {
    const mod = await import("../app/auth/sign-in/page");
    expect(typeof mod.default).toBe("function");
  });
});

describe("app/auth/sign-up/page.tsx", () => {
  it("exports a default React component", async () => {
    const mod = await import("../app/auth/sign-up/page");
    expect(typeof mod.default).toBe("function");
  });
});

describe("app/auth/forgot-password/page.tsx", () => {
  it("exports a default React component", async () => {
    const mod = await import("../app/auth/forgot-password/page");
    expect(typeof mod.default).toBe("function");
  });
});

describe("app/auth/reset-password/page.tsx", () => {
  it("exports a default React component", async () => {
    const mod = await import("../app/auth/reset-password/page");
    expect(typeof mod.default).toBe("function");
  });
});
