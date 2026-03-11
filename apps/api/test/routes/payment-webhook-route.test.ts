import { describe, expect, it, vi } from "vitest";
import { registerPaymentWebhookRoute } from "../../src/routes/payment-webhook";

type Handler = (request?: { body?: unknown; headers?: Record<string, string> }, reply?: { code: (status: number) => { send: (payload: unknown) => unknown } }) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, Handler>();

  post(path: string, handler: Handler) {
    this.handlers.set(path, handler);
  }
}

describe("payment webhook route", () => {
  it("rejects unsupported provider", async () => {
    const app = new FakeApp();
    registerPaymentWebhookRoute(app as never, {
      adapters: {
        bkash: { verifyWebhook: vi.fn() },
        sslcommerz: { verifyWebhook: vi.fn() },
      },
      payments: { applyWebhook: vi.fn() },
    } as never);

    const handler = app.handlers.get("/v1/payments/webhook");
    const reply = { code: () => ({ send: (payload: unknown) => payload }) };
    const result = (await handler?.(
      { body: { provider: "other", intent_id: "i", provider_txn_id: "t", verified: true }, headers: {} },
      reply,
    )) as { error: string };

    expect(result.error).toBe("unsupported provider");
  });

  it("returns 409 for duplicate webhook claims instead of throwing", async () => {
    const app = new FakeApp();
    registerPaymentWebhookRoute(app as never, {
      adapters: {
        bkash: { verifyWebhook: vi.fn().mockResolvedValue(true) },
        sslcommerz: { verifyWebhook: vi.fn() },
      },
      payments: {
        applyWebhook: vi.fn().mockRejectedValue(new Error("payment intent claim failed: duplicate callback (intent_id=i, provider=bkash, provider_txn_id=t)")),
      },
    } as never);

    const handler = app.handlers.get("/v1/payments/webhook");
    const reply = { code: () => ({ send: (payload: unknown) => payload }) };
    const result = (await handler?.(
      { body: { provider: "bkash", intent_id: "i", provider_txn_id: "t", verified: true }, headers: {} },
      reply,
    )) as { error: string };

    expect(result.error).toMatch(/duplicate callback/i);
  });
});
