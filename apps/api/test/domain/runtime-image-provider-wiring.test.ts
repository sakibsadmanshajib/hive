import { afterEach, describe, expect, it, vi } from "vitest";

describe("runtime image provider wiring", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it("wires image-basic to the OpenAI provider and model mapping", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    let capturedConfig: Record<string, unknown> | undefined;

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
            chatModel: "gpt-4o-mini",
            imageModel: "gpt-image-1",
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
      SupabaseBillingStore: class {},
    }));
    vi.doMock("../../src/runtime/supabase-api-key-store", () => ({
      SupabaseApiKeyStore: class {},
    }));
    vi.doMock("../../src/runtime/supabase-user-store", () => ({
      SupabaseUserStore: class {},
    }));
    vi.doMock("../../src/providers/registry", () => ({
      ProviderRegistry: class {
        constructor(config: Record<string, unknown>) {
          capturedConfig = config;
        }
        captureStartupReadiness = vi.fn(async () => ({
          ollama: { ready: true, detail: "startup model ready" },
          groq: { ready: true, detail: "startup model ready" },
          openai: { ready: true, detail: "startup model ready" },
          mock: { ready: true, detail: "startup model ready" },
        }));
        status = vi.fn(async () => ({ providers: [] }));
        chat = vi.fn();
        imageGeneration = vi.fn();
        metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
        metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");

    createRuntimeServices();

    expect(capturedConfig?.modelProviderMap).toMatchObject({
      "image-basic": "openai",
    });
    expect(capturedConfig?.providerModelMap).toMatchObject({
      openai: "gpt-image-1",
    });
  });
});
