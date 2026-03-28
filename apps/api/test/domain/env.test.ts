import { afterEach, describe, expect, it } from "vitest";
import { getEnv } from "../../src/config/env";

const trackedKeys = [
  "SUPABASE_URL",
  "SUPABASE_SERVICE_ROLE_KEY",
  "SUPABASE_AUTH_ENABLED",
  "SUPABASE_USER_REPO_ENABLED",
  "SUPABASE_API_KEYS_ENABLED",
  "SUPABASE_BILLING_STORE_ENABLED",
  "PAYMENT_RECONCILIATION_ENABLED",
  "PAYMENT_RECONCILIATION_INTERVAL_MS",
  "PAYMENT_RECONCILIATION_LOOKBACK_HOURS",
  "PROVIDER_TIMEOUT_MS",
  "PROVIDER_MAX_RETRIES",
  "OPENROUTER_API_KEY",
  "OPENROUTER_BASE_URL",
  "OPENROUTER_MODEL",
  "OPENROUTER_FREE_MODEL",
  "OPENROUTER_FREE_EMBEDDING_MODEL",
  "OPENAI_API_KEY",
  "OPENAI_BASE_URL",
  "OPENAI_CHAT_MODEL",
  "OPENAI_IMAGE_MODEL",
  "OLLAMA_FREE_MODEL",
  "OLLAMA_TIMEOUT_MS",
  "OLLAMA_MAX_RETRIES",
  "GROQ_TIMEOUT_MS",
  "GROQ_MAX_RETRIES",
  "GEMINI_API_KEY",
  "GEMINI_BASE_URL",
  "GEMINI_MODEL",
  "ANTHROPIC_API_KEY",
  "ANTHROPIC_BASE_URL",
  "ANTHROPIC_MODEL",
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
    expect(env.paymentReconciliation.enabled).toBe(false);
    expect(env.paymentReconciliation.intervalMs).toBe(60 * 60 * 1000);
    expect(env.paymentReconciliation.lookbackHours).toBe(24);
  });
});

describe("getEnv payment reconciliation config", () => {
  it("reads payment reconciliation scheduler settings", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.PAYMENT_RECONCILIATION_ENABLED = "true";
    process.env.PAYMENT_RECONCILIATION_INTERVAL_MS = "120000";
    process.env.PAYMENT_RECONCILIATION_LOOKBACK_HOURS = "48";

    const env = getEnv();

    expect(env.paymentReconciliation.enabled).toBe(true);
    expect(env.paymentReconciliation.intervalMs).toBe(120000);
    expect(env.paymentReconciliation.lookbackHours).toBe(48);
  });

  it("rejects invalid payment reconciliation booleans", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.PAYMENT_RECONCILIATION_ENABLED = "notabool";

    expect(() => getEnv()).toThrowError(/PAYMENT_RECONCILIATION_ENABLED/);
  });

  it("rejects invalid payment reconciliation interval values", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.PAYMENT_RECONCILIATION_INTERVAL_MS = "0";

    expect(() => getEnv()).toThrowError(/PAYMENT_RECONCILIATION_INTERVAL_MS/);
  });

  it("rejects invalid payment reconciliation lookback values", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.PAYMENT_RECONCILIATION_LOOKBACK_HOURS = "12.5";

    expect(() => getEnv()).toThrowError(/PAYMENT_RECONCILIATION_LOOKBACK_HOURS/);
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

  it("reads an explicit zero-cost Ollama model without making it implicit", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.OLLAMA_FREE_MODEL = "qwen2.5:0.5b";

    const env = getEnv();

    expect(env.providers.ollama.model).toBe("llama3.1:8b");
    expect(env.providers.ollama.freeModel).toBe("qwen2.5:0.5b");
  });

  it("treats empty timeout overrides as unset and falls back to shared timeout", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.PROVIDER_TIMEOUT_MS = "4500";
    process.env.OLLAMA_TIMEOUT_MS = "";
    process.env.GROQ_TIMEOUT_MS = "";

    const env = getEnv();

    expect(env.providers.ollama.timeoutMs).toBe(4500);
    expect(env.providers.groq.timeoutMs).toBe(4500);
  });

  it("rejects non-positive provider timeout values", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.PROVIDER_TIMEOUT_MS = "0";

    expect(() => getEnv()).toThrowError(/PROVIDER_TIMEOUT_MS/);
  });

  it("reads hosted provider chat configuration for openrouter, openai, gemini, and anthropic", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.OPENROUTER_API_KEY = "openrouter-key";
    process.env.OPENROUTER_BASE_URL = "https://openrouter.ai/api/v1";
    process.env.OPENROUTER_FREE_MODEL = "openrouter/free-model";
    process.env.OPENAI_API_KEY = "openai-key";
    process.env.OPENAI_BASE_URL = "https://api.openai.com/v1";
    process.env.OPENAI_CHAT_MODEL = "gpt-4o-mini";
    process.env.OPENAI_IMAGE_MODEL = "gpt-image-1";
    process.env.GEMINI_API_KEY = "gemini-key";
    process.env.GEMINI_BASE_URL = "https://generativelanguage.googleapis.com/v1beta/openai/";
    process.env.GEMINI_MODEL = "gemini-2.5-flash";
    process.env.ANTHROPIC_API_KEY = "anthropic-key";
    process.env.ANTHROPIC_BASE_URL = "https://api.anthropic.com/v1";
    process.env.ANTHROPIC_MODEL = "claude-sonnet-4-20250514";

    const env = getEnv();
    const providers = env.providers as Record<string, any>;

    expect(providers.openrouter).toMatchObject({
      apiKey: "openrouter-key",
      baseUrl: "https://openrouter.ai/api/v1",
      model: "openrouter/free-model",
    });
    expect(providers.openai).toMatchObject({
      apiKey: "openai-key",
      baseUrl: "https://api.openai.com/v1",
      chatModel: "gpt-4o-mini",
      imageModel: "gpt-image-1",
    });
    expect(providers.gemini).toMatchObject({
      apiKey: "gemini-key",
      baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai/",
      model: "gemini-2.5-flash",
    });
    expect(providers.anthropic).toMatchObject({
      apiKey: "anthropic-key",
      baseUrl: "https://api.anthropic.com/v1",
      model: "claude-sonnet-4-20250514",
    });
  });

  it("treats blank OpenRouter model env vars as unset and falls back to the router default", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.OPENROUTER_MODEL = "";
    process.env.OPENROUTER_FREE_MODEL = "";

    const env = getEnv();

    expect(env.providers.openrouter.model).toBe("openrouter/auto");
    expect(env.providers.openrouter.freeModel).toBeUndefined();
  });

  it("reads an explicit verification-only OpenRouter embedding model without changing the chat default", () => {
    process.env.SUPABASE_URL = "https://demo.supabase.co";
    process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
    process.env.OPENROUTER_FREE_EMBEDDING_MODEL = "nvidia/llama-nemotron-embed-vl-1b-v2:free";
    delete process.env.OPENROUTER_MODEL;
    delete process.env.OPENROUTER_FREE_MODEL;

    const env = getEnv();

    expect(env.providers.openrouter.model).toBe("openrouter/auto");
    expect(env.providers.openrouter.freeEmbeddingModel).toBe("nvidia/llama-nemotron-embed-vl-1b-v2:free");
  });
});
