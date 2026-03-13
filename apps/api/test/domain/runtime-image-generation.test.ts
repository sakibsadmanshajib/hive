import { afterEach, describe, expect, it, vi } from "vitest";

describe("runtime image generation billing", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it("refunds credits when the provider image request fails", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const consumeCredits = vi.fn(async () => true);
    const refundCredits = vi.fn(async () => ({
      userId: "user-1",
      availableCredits: 120,
      purchasedCredits: 120,
      promoCredits: 0,
    }));

    vi.doMock("../../src/config/env", () => ({
      getEnv: () => ({
        nodeEnv: "test",
        port: 8080,
        postgresUrl: "postgres://test",
        redisUrl: "redis://127.0.0.1:6379",
        rateLimitPerMinute: 60,
        adminStatusToken: "admin",
        allowDemoPaymentConfirm: true,
        allowDevApiKeyPrefix: false,
        google: { clientId: "id", clientSecret: "secret", redirectUri: "callback" },
        auth: {
          sessionTtlMinutes: 60,
          enforceTwoFactorSensitiveActions: false,
          twoFactorVerificationWindowMinutes: 10,
        },
        webhookSecrets: { bkash: "bk", sslcommerz: "ssl" },
        bkash: { verifyEndpoint: "", bearerToken: "" },
        sslcommerz: { validatorEndpoint: "", storeId: "", storePassword: "" },
        supabase: {
          url: "https://demo.supabase.co",
          serviceRoleKey: "service-role-key",
          flags: {
            authEnabled: false,
            userRepoEnabled: false,
            apiKeysEnabled: false,
            billingStoreEnabled: true,
          },
        },
        paymentReconciliation: {
          enabled: false,
          intervalMs: 60000,
          lookbackHours: 24,
        },
        providers: {
          ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
          groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 50, maxRetries: 0 },
          openai: {
            baseUrl: "https://api.openai.com/v1",
            apiKey: "test-key",
            model: "gpt-image-1",
            timeoutMs: 50,
            maxRetries: 0,
          },
          circuitBreaker: { failureThreshold: 5, resetTimeoutMs: 100 },
        },
        langfuse: {
          enabled: false,
          baseUrl: "https://cloud.langfuse.com",
          publicKey: undefined,
          secretKey: undefined,
        },
      }),
    }));

    vi.doMock("../../src/runtime/postgres-store", () => ({
      PostgresStore: class {},
    }));
    vi.doMock("../../src/runtime/supabase-client", () => ({
      createSupabaseAdminClient: () => ({ from: () => ({}) }),
    }));
    vi.doMock("../../src/runtime/supabase-billing-store", () => ({
      SupabaseBillingStore: class {
        consumeCredits = consumeCredits;
        refundCredits = refundCredits;
      },
    }));
    vi.doMock("../../src/runtime/supabase-api-key-store", () => ({
      SupabaseApiKeyStore: class {},
    }));
    vi.doMock("../../src/runtime/supabase-user-store", () => ({
      SupabaseUserStore: class {},
    }));
    vi.doMock("../../src/providers/registry", () => ({
      ProviderRegistry: class {
        captureStartupReadiness = vi.fn(async () => ({
          ollama: { ready: true, detail: "startup model ready" },
          groq: { ready: true, detail: "startup model ready" },
          openai: { ready: true, detail: "startup model ready" },
          mock: { ready: true, detail: "startup model ready" },
        }));
        status = vi.fn(async () => ({ providers: [] }));
        chat = vi.fn();
        imageGeneration = vi.fn(async () => {
          throw new Error("provider down");
        });
        metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
        metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    const result = await services.ai.imageGeneration("user-1", {
      model: "image-basic",
      prompt: "a lighthouse in fog",
      responseFormat: "url",
    });

    expect(result).toMatchObject({
      statusCode: 502,
      error: "provider unavailable",
    });
    expect(consumeCredits).toHaveBeenCalledTimes(1);
    expect(refundCredits).toHaveBeenCalledTimes(1);
  });

  it("does not call the provider when credit consumption fails", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const consumeCredits = vi.fn(async () => false);
    const providerImageGeneration = vi.fn();

    vi.doMock("../../src/config/env", () => ({
      getEnv: () => ({
        nodeEnv: "test",
        port: 8080,
        postgresUrl: "postgres://test",
        redisUrl: "redis://127.0.0.1:6379",
        rateLimitPerMinute: 60,
        adminStatusToken: "admin",
        allowDemoPaymentConfirm: true,
        allowDevApiKeyPrefix: false,
        google: { clientId: "id", clientSecret: "secret", redirectUri: "callback" },
        auth: {
          sessionTtlMinutes: 60,
          enforceTwoFactorSensitiveActions: false,
          twoFactorVerificationWindowMinutes: 10,
        },
        webhookSecrets: { bkash: "bk", sslcommerz: "ssl" },
        bkash: { verifyEndpoint: "", bearerToken: "" },
        sslcommerz: { validatorEndpoint: "", storeId: "", storePassword: "" },
        supabase: {
          url: "https://demo.supabase.co",
          serviceRoleKey: "service-role-key",
          flags: {
            authEnabled: false,
            userRepoEnabled: false,
            apiKeysEnabled: false,
            billingStoreEnabled: true,
          },
        },
        paymentReconciliation: {
          enabled: false,
          intervalMs: 60000,
          lookbackHours: 24,
        },
        providers: {
          ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
          groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 50, maxRetries: 0 },
          openai: {
            baseUrl: "https://api.openai.com/v1",
            apiKey: "test-key",
            model: "gpt-image-1",
            timeoutMs: 50,
            maxRetries: 0,
          },
          circuitBreaker: { failureThreshold: 5, resetTimeoutMs: 100 },
        },
        langfuse: {
          enabled: false,
          baseUrl: "https://cloud.langfuse.com",
          publicKey: undefined,
          secretKey: undefined,
        },
      }),
    }));

    vi.doMock("../../src/runtime/postgres-store", () => ({
      PostgresStore: class {},
    }));
    vi.doMock("../../src/runtime/supabase-client", () => ({
      createSupabaseAdminClient: () => ({ from: () => ({}) }),
    }));
    vi.doMock("../../src/runtime/supabase-billing-store", () => ({
      SupabaseBillingStore: class {
        consumeCredits = consumeCredits;
      },
    }));
    vi.doMock("../../src/runtime/supabase-api-key-store", () => ({
      SupabaseApiKeyStore: class {},
    }));
    vi.doMock("../../src/runtime/supabase-user-store", () => ({
      SupabaseUserStore: class {},
    }));
    vi.doMock("../../src/providers/registry", () => ({
      ProviderRegistry: class {
        captureStartupReadiness = vi.fn(async () => ({
          ollama: { ready: true, detail: "startup model ready" },
          groq: { ready: true, detail: "startup model ready" },
          openai: { ready: true, detail: "startup model ready" },
          mock: { ready: true, detail: "startup model ready" },
        }));
        status = vi.fn(async () => ({ providers: [] }));
        chat = vi.fn();
        imageGeneration = providerImageGeneration;
        metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
        metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    const result = await services.ai.imageGeneration("user-1", {
      model: "image-basic",
      prompt: "a lighthouse in fog",
      responseFormat: "url",
    });

    expect(result).toMatchObject({
      statusCode: 402,
      error: "insufficient credits",
    });
    expect(providerImageGeneration).not.toHaveBeenCalled();
  });
});
