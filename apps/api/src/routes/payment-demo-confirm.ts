import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

type DemoConfirmBody = {
  intent_id: string;
  provider_txn_id?: string;
};

function mapPaymentClaimError(error: unknown): { status: number; payload: { error: string } } {
  const message = error instanceof Error ? error.message : "demo payment confirm failed";
  if (message.includes("intent not found")) {
    return { status: 404, payload: { error: "intent not found" } };
  }
  if (message.includes("duplicate") || message.includes("provider mismatch")) {
    return { status: 409, payload: { error: message } };
  }
  return { status: 500, payload: { error: "demo payment confirm failed" } };
}

export function registerPaymentDemoConfirmRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: DemoConfirmBody }>("/v1/payments/demo/confirm", async (request, reply) => {
    if (!services.env.allowDemoPaymentConfirm) {
      reply.code(403);
      return { error: "demo payment confirm disabled" };
    }
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "billing",
      requiredPermission: "billing:write",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
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
    if (intent.userId !== principal.userId) {
      reply.code(403);
      return { error: "intent does not belong to current user" };
    }
    let result;
    try {
      result = await services.payments.confirmDemoIntent({
        intentId,
        providerTxnId: request.body?.provider_txn_id,
      });
    } catch (error) {
      const mapped = mapPaymentClaimError(error);
      reply.code(mapped.status);
      return mapped.payload;
    }
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
