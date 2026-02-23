import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requireApiUser } from "./auth";

type PaymentIntentBody = {
  user_id?: string;
  bdt_amount?: number;
  provider?: "bkash" | "sslcommerz";
};

export function registerPaymentIntentsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: PaymentIntentBody }>("/v1/payments/intents", async (request, reply) => {
    const userId = await requireApiUser(request, reply, services, "billing");
    if (!userId) {
      return;
    }

    const intent = await services.payments.createIntent({
      userId,
      bdtAmount: Math.max(0, Number(request.body?.bdt_amount ?? 0)),
      provider: request.body?.provider ?? "bkash",
    });

    reply.code(201);
    return {
      intent_id: intent.intentId,
      provider: intent.provider,
      status: intent.status,
      redirect_url: `https://sandbox.pay/${intent.provider}/${intent.intentId}`,
    };
  });
}
