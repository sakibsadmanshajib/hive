// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { beforeEach, describe, expect, it, vi } from "vitest";

const getSessionMock = vi.fn();
const onAuthStateChangeMock = vi.fn();

vi.mock("@supabase/ssr", () => ({
  createBrowserClient: () => ({
    auth: {
      getSession: getSessionMock,
      onAuthStateChange: onAuthStateChangeMock,
    },
  }),
}));

describe("Supabase auth session sync", () => {
  beforeEach(() => {
    vi.resetModules();
    getSessionMock.mockReset();
    onAuthStateChangeMock.mockReset();
    window.localStorage.clear();
    process.env.NEXT_PUBLIC_SUPABASE_URL = "http://127.0.0.1:54321";
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY = "test-supabase-anon-key";
  });

  it("writes refreshed Supabase sessions into the custom auth store", async () => {
    let authStateHandler: ((event: string, session: unknown) => void) | undefined;
    getSessionMock.mockResolvedValue({
      data: {
        session: {
          access_token: "initial_token",
          user: {
            email: "demo@example.com",
            user_metadata: { name: "Demo" },
          },
        },
      },
    });
    onAuthStateChangeMock.mockImplementation((handler) => {
      authStateHandler = handler;
      return {
        data: {
          subscription: {
            unsubscribe: vi.fn(),
          },
        },
      };
    });

    const { ensureSupabaseAuthSessionSync } = await import("../src/lib/supabase-client");
    const { readAuthSession } = await import("../src/features/auth/auth-session");

    ensureSupabaseAuthSessionSync();
    await Promise.resolve();

    expect(readAuthSession()).toEqual({
      accessToken: "initial_token",
      email: "demo@example.com",
      name: "Demo",
    });

    authStateHandler?.("TOKEN_REFRESHED", {
      access_token: "rotated_token",
      user: {
        email: "demo@example.com",
        user_metadata: { name: "Demo" },
      },
    });

    expect(readAuthSession()).toEqual({
      accessToken: "rotated_token",
      email: "demo@example.com",
      name: "Demo",
    });
  });

  it("does not clear an existing custom session when Supabase has not observed a real session", async () => {
    let authStateHandler: ((event: string, session: unknown) => void) | undefined;
    getSessionMock.mockResolvedValue({
      data: {
        session: null,
      },
    });
    onAuthStateChangeMock.mockImplementation((handler) => {
      authStateHandler = handler;
      return {
        data: {
          subscription: {
            unsubscribe: vi.fn(),
          },
        },
      };
    });

    const { ensureSupabaseAuthSessionSync } = await import("../src/lib/supabase-client");
    const { readAuthSession, writeAuthSession } = await import("../src/features/auth/auth-session");

    writeAuthSession({
      accessToken: "seeded_token",
      email: "seeded@example.com",
      name: "Seeded",
    });

    ensureSupabaseAuthSessionSync();
    await Promise.resolve();

    expect(readAuthSession()).toEqual({
      accessToken: "seeded_token",
      email: "seeded@example.com",
      name: "Seeded",
    });

    authStateHandler?.("SIGNED_OUT", null);

    expect(readAuthSession()).toEqual({
      accessToken: "seeded_token",
      email: "seeded@example.com",
      name: "Seeded",
    });
  });

  it("clears the custom auth session on sign-out after observing a real Supabase session", async () => {
    let authStateHandler: ((event: string, session: unknown) => void) | undefined;
    getSessionMock.mockResolvedValue({
      data: {
        session: {
          access_token: "initial_token",
          user: {
            email: "demo@example.com",
            user_metadata: { name: "Demo" },
          },
        },
      },
    });
    onAuthStateChangeMock.mockImplementation((handler) => {
      authStateHandler = handler;
      return {
        data: {
          subscription: {
            unsubscribe: vi.fn(),
          },
        },
      };
    });

    const { ensureSupabaseAuthSessionSync } = await import("../src/lib/supabase-client");
    const { readAuthSession } = await import("../src/features/auth/auth-session");

    ensureSupabaseAuthSessionSync();
    await Promise.resolve();

    authStateHandler?.("SIGNED_OUT", null);

    expect(readAuthSession()).toBeNull();
  });
});
