import { describe, expect, it, vi } from "vitest";

import { CreditLedger } from "../../src/domain/credits-ledger";
import type { PaymentReconciliationSnapshot } from "../../src/domain/types";
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

  it("mints correct credits for 2-decimal BDT amounts", () => {
    const ledger = new CreditLedger();
    const service = new PaymentService(ledger);

    service.createIntent("intent-decimal", "user-3", 19.99);
    service.handleVerifiedEvent("bkash", "bkash-decimal", "intent-decimal", true);

    expect(ledger.balance("user-3").total).toBe(1999);
  });

  it("keeps webhook idempotency for duplicate provider_txn_id", async () => {
    vi.resetModules();
    vi.spyOn(console, "warn").mockImplementation(() => undefined);

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
        paymentReconciliation: {
          enabled: false,
          intervalMs: 60000,
          lookbackHours: 24,
        },
        providers: {
          ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
          groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 50, maxRetries: 0 },
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
        async listRecentSnapshot(_since: Date): Promise<PaymentReconciliationSnapshot> {
          return { intents: [], events: [] };
        }

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
