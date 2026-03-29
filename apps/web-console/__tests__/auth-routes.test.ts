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
    const req = new NextRequest("http://localhost:3000/auth/callback?code=abc123");
    await mod.GET(req as never);

    expect(createServerClient).toHaveBeenCalled();
    expect(mockExchangeCodeForSession).toHaveBeenCalledWith("abc123");
  });

  it("redirects to /console when next param is missing", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req = new NextRequest("http://localhost:3000/auth/callback?code=abc");
    await mod.GET(req as never);

    expect(mockRedirect).toHaveBeenCalledWith(
      expect.objectContaining({ href: expect.stringContaining("/console") })
    );
  });

  it("allows /auth/reset-password as a valid next target", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc&next=/auth/reset-password"
    );
    await mod.GET(req as never);

    expect(mockRedirect).toHaveBeenCalledWith(
      expect.objectContaining({ href: expect.stringContaining("/auth/reset-password") })
    );
  });

  it("rejects arbitrary next targets and falls back to /console", async () => {
    const { NextRequest } = await import("next/server");
    mockExchangeCodeForSession.mockResolvedValueOnce({ error: null });

    const mod = await import("../app/auth/callback/route");
    const req = new NextRequest(
      "http://localhost:3000/auth/callback?code=abc&next=/evil-redirect"
    );
    await mod.GET(req as never);

    expect(mockRedirect).toHaveBeenCalledWith(
      expect.objectContaining({ href: expect.stringContaining("/console") })
    );
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
