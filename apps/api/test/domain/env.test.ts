import { afterEach, describe, expect, it } from "vitest";
import { getEnv } from "../../src/config/env";

const trackedKeys = [
  "SUPABASE_URL",
  "SUPABASE_SERVICE_ROLE_KEY",
  "SUPABASE_AUTH_ENABLED",
  "SUPABASE_USER_REPO_ENABLED",
  "SUPABASE_API_KEYS_ENABLED",
  "SUPABASE_BILLING_STORE_ENABLED",
  "PROVIDER_TIMEOUT_MS",
  "PROVIDER_MAX_RETRIES",
  "OLLAMA_TIMEOUT_MS",
  "OLLAMA_MAX_RETRIES",
  "GROQ_TIMEOUT_MS",
  "GROQ_MAX_RETRIES",
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

describe("getEnv provider timeout and retry controls", () => {
  it("defaults provider timeout and retries to safe values", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    delete process.env.PROVIDER_TIMEOUT_MS;
    delete process.env.PROVIDER_MAX_RETRIES;
    delete process.env.OLLAMA_TIMEOUT_MS;
    delete process.env.OLLAMA_MAX_RETRIES;
    delete process.env.GROQ_TIMEOUT_MS;
    delete process.env.GROQ_MAX_RETRIES;

    const env = getEnv();

    expect(env.providers.ollama.timeoutMs).toBe(4000);
    expect(env.providers.ollama.maxRetries).toBe(1);
    expect(env.providers.groq.timeoutMs).toBe(4000);
    expect(env.providers.groq.maxRetries).toBe(1);
  });

  it("supports shared and provider-specific timeout and retry overrides", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.PROVIDER_TIMEOUT_MS = "5500";
    process.env.PROVIDER_MAX_RETRIES = "2";
    process.env.OLLAMA_TIMEOUT_MS = "3200";
    process.env.OLLAMA_MAX_RETRIES = "0";
    process.env.GROQ_TIMEOUT_MS = "7100";
    process.env.GROQ_MAX_RETRIES = "3";

    const env = getEnv();

    expect(env.providers.ollama.timeoutMs).toBe(3200);
    expect(env.providers.ollama.maxRetries).toBe(0);
    expect(env.providers.groq.timeoutMs).toBe(7100);
    expect(env.providers.groq.maxRetries).toBe(3);
  });
});
