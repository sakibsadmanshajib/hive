/**
 * TDD: Supabase client helper contracts.
 *
 * These tests verify:
 * 1. browser.ts exports a factory that calls createBrowserClient
 * 2. server.ts exports a factory that calls createServerClient
 * 3. Both helpers read the correct env vars
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

// --- mock @supabase/ssr before importing helpers ---
const mockBrowserClient = { from: vi.fn(), auth: {} };
const mockServerClient = { from: vi.fn(), auth: {} };

const createBrowserClientMock = vi.fn(() => mockBrowserClient);
const createServerClientMock = vi.fn(() => mockServerClient);

vi.mock("@supabase/ssr", () => ({
  createBrowserClient: createBrowserClientMock,
  createServerClient: createServerClientMock,
}));

// Stub env vars
const SUPABASE_URL = "https://test.supabase.co";
const SUPABASE_ANON_KEY = "anon-key-test";

beforeEach(() => {
  vi.resetModules();
  createBrowserClientMock.mockClear();
  createServerClientMock.mockClear();
  process.env.NEXT_PUBLIC_SUPABASE_URL = SUPABASE_URL;
  process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY = SUPABASE_ANON_KEY;
});

describe("lib/supabase/browser.ts", () => {
  it("exports a createClient function", async () => {
    const mod = await import("../lib/supabase/browser");
    expect(typeof mod.createClient).toBe("function");
  });

  it("calls createBrowserClient with correct env vars", async () => {
    const { createBrowserClient } = await import("@supabase/ssr");
    const mod = await import("../lib/supabase/browser");
    mod.createClient();
    expect(createBrowserClient).toHaveBeenCalledWith(
      SUPABASE_URL,
      SUPABASE_ANON_KEY
    );
  });
});

describe("lib/supabase/server.ts", () => {
  it("exports a createClient function", async () => {
    const mod = await import("../lib/supabase/server");
    expect(typeof mod.createClient).toBe("function");
  });

  it("calls createServerClient with correct env vars", async () => {
    const { createServerClient } = await import("@supabase/ssr");
    const cookieStore = {
      getAll: vi.fn(() => []),
      set: vi.fn(),
    };
    const mod = await import("../lib/supabase/server");
    mod.createClient(cookieStore as never);
    expect(createServerClient).toHaveBeenCalledWith(
      SUPABASE_URL,
      SUPABASE_ANON_KEY,
      expect.objectContaining({ cookies: expect.any(Object) })
    );
  });
});
