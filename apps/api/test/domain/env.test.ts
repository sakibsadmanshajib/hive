import { afterEach, describe, expect, it } from "vitest";
import { getEnv } from "../../src/config/env";

const trackedKeys = [
  "SUPABASE_URL",
  "SUPABASE_SERVICE_ROLE_KEY",
  "SUPABASE_AUTH_ENABLED",
  "SUPABASE_USER_REPO_ENABLED",
  "SUPABASE_API_KEYS_ENABLED",
  "SUPABASE_BILLING_STORE_ENABLED",
] as const;

const originalValues = new Map<string, string | undefined>(
  trackedKeys.map((key) => [key, process.env[key]]),
);

afterEach(() => {
  for (const key of trackedKeys) {
    const original = originalValues.get(key);
    if (original === undefined) {
      delete process.env[key];
      continue;
    }
    process.env[key] = original;
  }
});

describe("getEnv supabase config", () => {
  it("reads supabase config and feature flags", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.SUPABASE_AUTH_ENABLED = "true";
    process.env.SUPABASE_USER_REPO_ENABLED = "yes";
    process.env.SUPABASE_API_KEYS_ENABLED = "1";
    process.env.SUPABASE_BILLING_STORE_ENABLED = "true";

    const env = getEnv();

    expect(env.supabase.url).toBe("https://demo.supabase.co");
    expect(env.supabase.serviceRoleKey).toBe("service-role-key");
    expect(env.supabase.flags.authEnabled).toBe(true);
    expect(env.supabase.flags.userRepoEnabled).toBe(true);
    expect(env.supabase.flags.apiKeysEnabled).toBe(true);
    expect(env.supabase.flags.billingStoreEnabled).toBe(true);
  });

  it("defaults all supabase flags to false", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    delete process.env.SUPABASE_AUTH_ENABLED;
    delete process.env.SUPABASE_USER_REPO_ENABLED;
    delete process.env.SUPABASE_API_KEYS_ENABLED;
    delete process.env.SUPABASE_BILLING_STORE_ENABLED;

    const env = getEnv();

    expect(env.supabase.flags.authEnabled).toBe(false);
    expect(env.supabase.flags.userRepoEnabled).toBe(false);
    expect(env.supabase.flags.apiKeysEnabled).toBe(false);
    expect(env.supabase.flags.billingStoreEnabled).toBe(false);
  });
});
