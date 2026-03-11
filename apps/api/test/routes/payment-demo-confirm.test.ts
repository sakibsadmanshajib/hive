import { describe, expect, it, vi } from "vitest";
import { registerPaymentDemoConfirmRoute } from "../../src/routes/payment-demo-confirm";

vi.mock("../../src/routes/auth", () => ({
  requirePrincipal: vi.fn().mockResolvedValue({ userId: "user-1" }),
}));

type Handler = (request?: { body?: unknown }, reply?: { code: (status: number) => unknown }) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, Handler>();

  post(path: string, handler: Handler) {
    this.handlers.set(path, handler);
  }
}

describe("payment demo confirm route", () => {
  it("returns 403 when demo confirm disabled", async () => {
    const app = new FakeApp();
    registerPaymentDemoConfirmRoute(app as never, {
      env: { allowDemoPaymentConfirm: false },
      payments: { confirmDemoIntent: vi.fn() },
    } as never);

    const handler = app.handlers.get("/v1/payments/demo/confirm");
    const result = (await handler?.({ body: { intent_id: "intent_1" } }, { code: () => undefined })) as { error: string };
    expect(result.error).toBe("demo payment confirm disabled");
  });

  it("returns 409 when demo confirm hits a duplicate claim", async () => {
    const app = new FakeApp();
    const codeMock = vi.fn();
    registerPaymentDemoConfirmRoute(app as never, {
      env: { allowDemoPaymentConfirm: true },
      payments: {
        getIntent: vi.fn().mockResolvedValue({ intentId: "intent_1", userId: "user-1" }),
        confirmDemoIntent: vi.fn().mockRejectedValue(new Error("payment intent claim failed: duplicate callback (intent_id=intent_1, provider=bkash, provider_txn_id=demo)")),
      },
    } as never);

    const handler = app.handlers.get("/v1/payments/demo/confirm");
    const result = (await handler?.(
      { body: { intent_id: "intent_1", provider_txn_id: "demo" } },
      { code: codeMock },
    )) as { error: string };

    expect(codeMock).toHaveBeenCalledWith(409);
    expect(result.error).toMatch(/duplicate callback/i);
  });
});
