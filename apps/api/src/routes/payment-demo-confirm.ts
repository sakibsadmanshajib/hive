import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requireApiUser } from "./auth";

type DemoConfirmBody = {
  intent_id: string;
  provider_txn_id?: string;
};

export function registerPaymentDemoConfirmRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: DemoConfirmBody }>("/v1/payments/demo/confirm", async (request, reply) => {
    if (!services.env.allowDemoPaymentConfirm) {
      reply.code(403);
      return { error: "demo payment confirm disabled" };
    }
    const userId = await requireApiUser(request, reply, services, "billing");
    if (!userId) {
      return;
    }
    const intentId = request.body?.intent_id;
    if (!intentId) {
      reply.code(400);
      return { error: "intent_id required" };
    }
    const intent = await services.payments.getIntent(intentId);
    if (!intent) {
      reply.code(404);
      return { error: "intent not found" };
    }
    if (intent.userId !== userId) {
      reply.code(403);
      return { error: "intent does not belong to current user" };
    }
    const result = await services.payments.confirmDemoIntent({
      intentId,
      providerTxnId: request.body?.provider_txn_id,
    });
    if (!result) {
      reply.code(404);
      return { error: "intent not found" };
    }
    return {
      status: "credited",
      intent_id: result.intentId,
      user_id: result.userId,
      minted_credits: result.mintedCredits,
    };
  });
}
