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

    const claimPaymentIntent = vi
      .fn<(...args: unknown[]) => Promise<{ success: boolean; intent?: any; error?: string }>>()
      .mockResolvedValueOnce({
        success: true,
        intent: {
          intentId: "intent-1",
          userId: "user-1",
          provider: "bkash",
          bdtAmount: 10,
          status: "credited",
          mintedCredits: 1000,
        },
      })
      .mockResolvedValueOnce({ success: false, error: "duplicate callback" });

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
      PostgresStore: class { },
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

        async createPaymentIntent(intent: any) { }
        async getPaymentIntent(intentId: string) {
          return { intentId, status: "credited" };
        }
        async markPaymentCredited() { }

        claimPaymentIntent = claimPaymentIntent;
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    // Since `payments` requires a real DB or precise mock, we simulate webhook processing directly:
    const res1 = await services.payments.applyWebhook({
      provider: "bkash",
      intent_id: "intent-1",
      provider_txn_id: "txn-123",
      verified: true,
    });
    expect(res1?.status).toBe("credited");
    await expect(
      services.payments.applyWebhook({
        provider: "bkash",
        intent_id: "intent-1",
        provider_txn_id: "txn-123",
        verified: true,
      }),
    ).rejects.toThrow(/duplicate callback/);
    expect(claimPaymentIntent).toHaveBeenCalledTimes(2);
  });
});
