import { describe, expect, it, vi } from "vitest";

import { CreditLedger } from "../../src/domain/credits-ledger";

describe("CreditLedger", () => {
  it("reserves then settles using actual credits", () => {
    const ledger = new CreditLedger();
    const userId = "user-1";

    ledger.mintPurchased(userId, 1000, "pay-1");
    const reservationId = ledger.reserve(userId, "req-1", 250);
    ledger.settle(reservationId, 180);

    const balance = ledger.balance(userId);
    expect(balance.total).toBe(820);
    expect(balance.reserved).toBe(0);
    expect(balance.available).toBe(820);
  });

  it("fails reservation when available credits are insufficient", () => {
    const ledger = new CreditLedger();
    const userId = "user-1";

    ledger.mintPurchased(userId, 1000, "pay-1");

    expect(() => ledger.reserve(userId, "req-2", 4000)).toThrowError(
      "insufficient credits",
    );
  });

  it("consumes purchased first then promo credits", () => {
    const ledger = new CreditLedger();
    const userId = "user-1";

    ledger.mintPurchased(userId, 100, "pay-1");
    ledger.mintPromo(userId, 50, "eid");
    ledger.consume(userId, "req-1", 120);

    const balance = ledger.balance(userId);
    expect(balance.total).toBe(30);
    expect(balance.available).toBe(30);
  });

  it("applies 1 BDT = 100 credits using supabase store path", async () => {
    vi.resetModules();

    const topUpCredits = vi.fn(async () => ({
      userId: "user-1",
      availableCredits: 1000,
      purchasedCredits: 1000,
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
        google: { clientId: "id", clientSecret: "secret", redirectUri: "http://127.0.0.1/callback" },
        auth: {
          sessionTtlMinutes: 60,
          enforceTwoFactorSensitiveActions: false,
          twoFactorVerificationWindowMinutes: 10,
        },
        webhookSecrets: { bkash: "bk", sslcommerz: "ssl" },
        bkash: {},
        sslcommerz: {},
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
          ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b" },
          groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant" },
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
      PostgresStore: class {
        async topUp() {
          throw new Error("postgres topUp should not be used when supabase billing store is enabled");
        }
      },
    }));

    vi.doMock("../../src/runtime/supabase-client", () => ({
      createSupabaseAdminClient: () => ({ from: () => ({}) }),
    }));

    vi.doMock("../../src/runtime/supabase-billing-store", () => ({
      SupabaseBillingStore: class {
        async getBalance() {
          return { userId: "user-1", availableCredits: 0, purchasedCredits: 0, promoCredits: 0 };
        }

        async consumeCredits() {
          return true;
        }

        topUpCredits = topUpCredits;

        async createPaymentIntent() {
          return undefined;
        }

        async getPaymentIntent() {
          return undefined;
        }

        async recordPaymentEvent() {
          return true;
        }

        async listRecentSnapshot() {
          return { intents: [], events: [] };
        }

        async markPaymentCredited() {
          return undefined;
        }
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    await services.credits.topUp("user-1", 10, "payment_intent_1");

    expect(topUpCredits).toHaveBeenCalledWith("user-1", 1000, "payment_intent_1");
  });

  it("rounds decimal BDT amounts to the correct credit value in the supabase store path", async () => {
    vi.resetModules();

    const topUpCredits = vi.fn(async () => ({
      userId: "user-1",
      availableCredits: 1999,
      purchasedCredits: 1999,
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
        google: { clientId: "id", clientSecret: "secret", redirectUri: "http://127.0.0.1/callback" },
        auth: {
          sessionTtlMinutes: 60,
          enforceTwoFactorSensitiveActions: false,
          twoFactorVerificationWindowMinutes: 10,
        },
        webhookSecrets: { bkash: "bk", sslcommerz: "ssl" },
        bkash: {},
        sslcommerz: {},
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
          ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b" },
          groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant" },
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
      PostgresStore: class { },
    }));

    vi.doMock("../../src/runtime/supabase-client", () => ({
      createSupabaseAdminClient: () => ({ from: () => ({}) }),
    }));

    vi.doMock("../../src/runtime/supabase-billing-store", () => ({
      SupabaseBillingStore: class {
        async getBalance() {
          return { userId: "user-1", availableCredits: 0, purchasedCredits: 0, promoCredits: 0 };
        }

        async consumeCredits() {
          return true;
        }

        topUpCredits = topUpCredits;

        async createPaymentIntent() {
          return undefined;
        }

        async getPaymentIntent() {
          return undefined;
        }

        async recordPaymentEvent() {
          return true;
        }

        async listRecentSnapshot() {
          return { intents: [], events: [] };
        }

        async markPaymentCredited() {
          return undefined;
        }
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    await services.credits.topUp("user-1", 19.99, "payment_intent_decimal");

    expect(topUpCredits).toHaveBeenCalledWith("user-1", 1999, "payment_intent_decimal");
  });
});
