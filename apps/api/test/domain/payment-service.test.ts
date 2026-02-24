import { describe, expect, it, vi } from "vitest";

import { CreditLedger } from "../../src/domain/credits-ledger";
import { PaymentService } from "../../src/domain/payment-service";

describe("PaymentService", () => {
  it("does not duplicate credits for duplicate provider callback", () => {
    const ledger = new CreditLedger();
    const service = new PaymentService(ledger);

    service.createIntent("intent-1", "user-1", 100);
    service.handleVerifiedEvent("bkash", "bkash-abc", "intent-1", true);
    service.handleVerifiedEvent("bkash", "bkash-abc", "intent-1", true);

    expect(ledger.balance("user-1").total).toBe(10000);
  });

  it("never mints credits for unverified event", () => {
    const ledger = new CreditLedger();
    const service = new PaymentService(ledger);

    service.createIntent("intent-2", "user-2", 100);
    service.handleVerifiedEvent("sslcommerz", "ssl-abc", "intent-2", false);

    expect(ledger.balance("user-2").total).toBe(0);
  });

  it("keeps webhook idempotency for duplicate provider_txn_id", async () => {
    vi.resetModules();

    const topUpCredits = vi.fn(async () => ({
      userId: "user-1",
      availableCredits: 1000,
      purchasedCredits: 1000,
      promoCredits: 0,
    }));
    const recordPaymentEvent = vi
      .fn<(...args: unknown[]) => Promise<boolean>>()
      .mockResolvedValueOnce(true)
      .mockResolvedValueOnce(false);
    const markPaymentCredited = vi.fn(async () => undefined);

    const intents = new Map<string, { intentId: string; userId: string; provider: "bkash" | "sslcommerz"; bdtAmount: number; status: "initiated" | "credited" | "failed"; mintedCredits: number }>();

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
      PostgresStore: class {},
    }));

    vi.doMock("../../src/runtime/supabase-client", () => ({
      createSupabaseAdminClient: () => ({ from: () => ({}) }),
    }));

    vi.doMock("../../src/runtime/supabase-billing-store", () => ({
      SupabaseBillingStore: class {
        async getBalance(userId: string) {
          return { userId, availableCredits: 0, purchasedCredits: 0, promoCredits: 0 };
        }

        async consumeCredits() {
          return true;
        }

        topUpCredits = topUpCredits;

        async createPaymentIntent(intent: {
          intentId: string;
          userId: string;
          provider: "bkash" | "sslcommerz";
          bdtAmount: number;
          status: "initiated" | "credited" | "failed";
          mintedCredits: number;
        }) {
          intents.set(intent.intentId, { ...intent });
        }

        async getPaymentIntent(intentId: string) {
          const intent = intents.get(intentId);
          return intent ? { ...intent } : undefined;
        }

        recordPaymentEvent = recordPaymentEvent;

        markPaymentCredited = markPaymentCredited;
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    const intent = await services.payments.createIntent({ userId: "user-1", provider: "bkash", bdtAmount: 10 });
    await services.payments.applyWebhook({
      provider: "bkash",
      intent_id: intent.intentId,
      provider_txn_id: "txn-123",
      verified: true,
    });
    await services.payments.applyWebhook({
      provider: "bkash",
      intent_id: intent.intentId,
      provider_txn_id: "txn-123",
      verified: true,
    });

    expect(recordPaymentEvent).toHaveBeenCalledTimes(2);
    expect(topUpCredits).toHaveBeenCalledTimes(1);
    expect(markPaymentCredited).toHaveBeenCalledWith(intent.intentId, 1000, "credited");
  });
});
