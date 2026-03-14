// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

const PUBLIC_ENV_KEYS = [
  "NEXT_PUBLIC_API_BASE_URL",
  "NEXT_PUBLIC_SUPABASE_URL",
  "NEXT_PUBLIC_SUPABASE_ANON_KEY",
] as const;

function clearPublicEnv() {
  for (const key of PUBLIC_ENV_KEYS) {
    delete process.env[key];
  }
}

afterEach(() => {
  vi.resetModules();
  process.env.NEXT_PUBLIC_API_BASE_URL = "http://127.0.0.1:8080";
  process.env.NEXT_PUBLIC_SUPABASE_URL = "http://127.0.0.1:54321";
  process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY = "test-supabase-anon-key";
  delete process.env.INTERNAL_API_BASE_URL;
});

describe("public env access", () => {
  it("does not throw when env-backed modules are imported without public env vars", async () => {
    clearPublicEnv();

    await expect(import("../src/lib/api")).resolves.toBeDefined();
    await expect(import("../src/app/auth/page")).resolves.toBeDefined();
  });

  it("still throws when runtime code actually requests a missing public env var", async () => {
    clearPublicEnv();

    const { getApiBase } = await import("../src/lib/api");

    expect(() => getApiBase()).toThrowError("NEXT_PUBLIC_API_BASE_URL is required");
  });

  it("uses INTERNAL_API_BASE_URL for server-side runtime code when provided", async () => {
    process.env.INTERNAL_API_BASE_URL = "http://api:8080";

    const { getServerApiBase } = await import("../src/lib/api");
    expect(getServerApiBase()).toBe("http://api:8080");
  });
});
