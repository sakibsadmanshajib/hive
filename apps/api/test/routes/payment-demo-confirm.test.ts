import { describe, expect, it, vi } from "vitest";
import { registerPaymentDemoConfirmRoute } from "../../src/routes/payment-demo-confirm";

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
});
