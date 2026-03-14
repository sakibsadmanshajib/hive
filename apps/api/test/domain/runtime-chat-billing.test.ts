import { afterEach, describe, expect, it, vi } from "vitest";

function createEnv() {
  return {
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
  };
}

function mockRuntime({
  consumeCredits,
  refundCredits,
  providerChat,
  usageSingle,
}: {
  consumeCredits: ReturnType<typeof vi.fn>;
  refundCredits: ReturnType<typeof vi.fn>;
  providerChat: ReturnType<typeof vi.fn>;
  usageSingle: ReturnType<typeof vi.fn>;
}) {
  vi.doMock("../../src/config/env", () => ({
    getEnv: () => createEnv(),
  }));
  vi.doMock("../../src/runtime/postgres-store", () => ({
    PostgresStore: class {},
  }));
  vi.doMock("../../src/runtime/supabase-client", () => ({
    createSupabaseAdminClient: () => ({
      from: (table: string) => {
        if (table === "usage_events") {
          return {
            insert: vi.fn().mockReturnValue({
              select: vi.fn().mockReturnValue({
                single: usageSingle,
              }),
            }),
          };
        }
        return {
          select: vi.fn().mockReturnValue({
            gte: vi.fn().mockResolvedValue({ data: [], error: null }),
          }),
        };
      },
    }),
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
      chat = providerChat;
      imageGeneration = vi.fn();
      metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
      metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
    },
  }));
}

describe("runtime chat billing", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it("refunds credits and skips usage writes when chat provider calls fail", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const consumeCredits = vi.fn(async () => true);
    const refundCredits = vi.fn(async () => ({
      userId: "user-1",
      availableCredits: 100,
      purchasedCredits: 100,
      promoCredits: 0,
    }));
    const providerChat = vi.fn(async () => {
      throw new Error("provider down");
    });
    const usageSingle = vi.fn();

    mockRuntime({ consumeCredits, refundCredits, providerChat, usageSingle });

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    const result = await services.ai.chatCompletions(
      "user-1",
      "fast-chat",
      [{ role: "user", content: "hello" }],
      { channel: "api" },
    );

    expect(result).toMatchObject({
      statusCode: 502,
      error: "provider down",
    });
    expect(consumeCredits).toHaveBeenCalledTimes(1);
    expect(refundCredits).toHaveBeenCalledTimes(1);
    expect(usageSingle).not.toHaveBeenCalled();
  });

  it("refunds credits and skips usage writes when responses provider calls fail", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const consumeCredits = vi.fn(async () => true);
    const refundCredits = vi.fn(async () => ({
      userId: "user-1",
      availableCredits: 100,
      purchasedCredits: 100,
      promoCredits: 0,
    }));
    const providerChat = vi.fn(async () => {
      throw new Error("provider down");
    });
    const usageSingle = vi.fn();

    mockRuntime({ consumeCredits, refundCredits, providerChat, usageSingle });

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    const result = await services.ai.responses("user-1", "hello", { channel: "api" });

    expect(result).toMatchObject({
      statusCode: 502,
      error: "provider down",
    });
    expect(consumeCredits).toHaveBeenCalledTimes(1);
    expect(refundCredits).toHaveBeenCalledTimes(1);
    expect(usageSingle).not.toHaveBeenCalled();
  });
});
