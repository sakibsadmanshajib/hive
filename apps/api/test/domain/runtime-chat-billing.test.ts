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
      openrouter: {
        apiKey: "openrouter-key",
        baseUrl: "https://openrouter.ai/api/v1",
        model: "openrouter/auto",
        freeModel: "openrouter/free-model",
        timeoutMs: 50,
        maxRetries: 0,
      },
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
      gemini: {
        apiKey: "gemini-key",
        baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai/",
        model: "gemini-2.5-flash",
        timeoutMs: 50,
        maxRetries: 0,
      },
      anthropic: {
        apiKey: "anthropic-key",
        baseUrl: "https://api.anthropic.com/v1",
        model: "claude-sonnet-4-20250514",
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
  guestUsageSingle,
}: {
  consumeCredits: ReturnType<typeof vi.fn>;
  refundCredits: ReturnType<typeof vi.fn>;
  providerChat: ReturnType<typeof vi.fn>;
  usageSingle: ReturnType<typeof vi.fn>;
  guestUsageSingle?: ReturnType<typeof vi.fn>;
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
        if (table === "guest_usage_events") {
          return {
            insert: vi.fn().mockReturnValue({
              select: vi.fn().mockReturnValue({
                single: guestUsageSingle ?? vi.fn().mockResolvedValue({
                  data: { created_at: "2026-03-14T00:00:00.000Z" },
                  error: null,
                }),
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

  it("bypasses credit consumption for authenticated zero-cost chat models", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const consumeCredits = vi.fn(async () => {
      throw new Error("consumeCredits should not be called for zero-cost models");
    });
    const refundCredits = vi.fn(async () => ({
      userId: "user-1",
      availableCredits: 100,
      purchasedCredits: 100,
      promoCredits: 0,
    }));
    const providerChat = vi.fn(async () => ({
      content: "provider-backed free reply",
      providerUsed: "openrouter",
      providerModel: "openrouter/free-model",
    }));
    const usageSingle = vi.fn(async () => ({
      data: {
        id: "usage-1",
        user_id: "user-1",
        endpoint: "/v1/chat/completions",
        model: "guest-free",
        credits: 0,
        channel: "api",
        api_key_id: null,
        created_at: "2026-03-14T00:00:00.000Z",
      },
      error: null,
    }));

    mockRuntime({ consumeCredits, refundCredits, providerChat, usageSingle });

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    await expect(
      services.ai.chatCompletions(
        "user-1",
        "guest-free",
        [{ role: "user", content: "hello" }],
        { channel: "api" },
      ),
    ).resolves.toMatchObject({
      statusCode: 200,
      headers: {
        "x-model-routed": "guest-free",
        "x-provider-used": "openrouter",
        "x-provider-model": "openrouter/free-model",
        "x-actual-credits": "0",
      },
      body: {
        choices: [{ message: { content: "provider-backed free reply" } }],
      },
    });

    expect(consumeCredits).not.toHaveBeenCalled();
    expect(refundCredits).not.toHaveBeenCalled();
    expect(providerChat).toHaveBeenCalledTimes(1);
    expect(usageSingle).toHaveBeenCalledTimes(1);
  });

  it("returns provider-backed guest-free completions without billing guest traffic", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const consumeCredits = vi.fn(async () => {
      throw new Error("consumeCredits should not be called for guest-free chat");
    });
    const refundCredits = vi.fn(async () => ({
      userId: "guest-1",
      availableCredits: 0,
      purchasedCredits: 0,
      promoCredits: 0,
    }));
    const providerChat = vi.fn(async () => ({
      content: "provider-backed guest reply",
      providerUsed: "openrouter",
      providerModel: "openrouter/free-model",
    }));
    const usageSingle = vi.fn();
    const guestUsageSingle = vi.fn(async () => ({
      data: {
        created_at: "2026-03-14T00:00:00.000Z",
      },
      error: null,
    }));

    mockRuntime({ consumeCredits, refundCredits, providerChat, usageSingle, guestUsageSingle });

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    await expect(
      services.ai.guestChatCompletions(
        "guest-1",
        "guest-free",
        [{ role: "user", content: "hello" }],
        "203.0.113.10",
      ),
    ).resolves.toMatchObject({
      statusCode: 200,
      headers: {
        "x-model-routed": "guest-free",
        "x-provider-used": "openrouter",
        "x-provider-model": "openrouter/free-model",
        "x-actual-credits": "0",
      },
      body: {
        choices: [{ message: { content: "provider-backed guest reply" } }],
      },
    });

    expect(consumeCredits).not.toHaveBeenCalled();
    expect(refundCredits).not.toHaveBeenCalled();
    expect(providerChat).toHaveBeenCalledTimes(1);
    expect(usageSingle).not.toHaveBeenCalled();
    expect(guestUsageSingle).toHaveBeenCalledTimes(1);
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
      "smart-reasoning",
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
