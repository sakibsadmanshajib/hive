import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";

type PaymentWebhookBody = {
  provider: "bkash" | "sslcommerz";
  intent_id: string;
  provider_txn_id: string;
  verified: boolean;
};

function mapPaymentClaimError(error: unknown): { status: number; payload: { error: string } } {
  const message = error instanceof Error ? error.message : "payment webhook failed";
  if (message.includes("intent not found")) {
    return { status: 404, payload: { error: "intent not found" } };
  }
  if (message.includes("duplicate") || message.includes("provider mismatch")) {
    return { status: 409, payload: { error: message } };
  }
  return { status: 500, payload: { error: "payment webhook failed" } };
}

export function registerPaymentWebhookRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: PaymentWebhookBody }>("/v1/payments/webhook", async (request, reply) => {
    const fallbackBody = JSON.stringify(request.body ?? {});
    const requestWithRaw = request as unknown as { rawBody?: unknown };
    const rawBody = typeof requestWithRaw.rawBody === "string" ? requestWithRaw.rawBody : fallbackBody;
    const provider = request.body?.provider;

    if (provider !== "bkash" && provider !== "sslcommerz") {
      return reply.code(400).send({ error: "unsupported provider" });
    }

    if (provider === "bkash") {
      const headers = Object.fromEntries(
        Object.entries(request.headers)
          .filter(([, value]) => typeof value === "string")
          .map(([key, value]) => [key, value as string]),
      );
      const verified = await services.adapters.bkash.verifyWebhook(headers, rawBody, request.body.provider_txn_id);
      if (!verified) {
        return reply.code(401).send({ error: "invalid signature" });
      }
    }

    if (provider === "sslcommerz") {
      const signature = request.headers["x-sslcommerz-signature"];
      const verified =
        typeof signature === "string" &&
        (await services.adapters.sslcommerz.verifyWebhook(request.body as Record<string, unknown>, signature));
      if (!verified) {
        return reply.code(401).send({ error: "invalid signature" });
      }
    }

    let intent;
    try {
      intent = await services.payments.applyWebhook({
        provider,
        intent_id: request.body.intent_id,
        provider_txn_id: request.body.provider_txn_id,
        verified: true,
      });
    } catch (error) {
      const mapped = mapPaymentClaimError(error);
      return reply.code(mapped.status).send(mapped.payload);
    }
    if (!intent) {
      return reply.code(404).send({ error: "intent not found" });
    }

    return { status: "accepted", intent_id: intent.intentId, payment_status: intent.status };
  });
}
