import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

type PaymentIntentBody = {
  user_id?: string;
  bdt_amount?: number;
  provider?: "bkash" | "sslcommerz";
};

export function registerPaymentIntentsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: PaymentIntentBody }>("/v1/payments/intents", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "billing",
      requiredPermission: "billing:write",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    if (services.env.auth.enforceTwoFactorSensitiveActions) {
      const settings = await services.userSettings.getForUser(principal.userId);
      if (settings.twoFactorEnabled) {
        const challengeId = request.headers["x-2fa-challenge-id"];
        if (typeof challengeId !== "string") {
          return reply.code(403).send({ error: "two-factor verification required" });
        }
        const verified = await services.twoFactor.hasRecentVerification(
          principal.userId,
          challengeId,
          services.env.auth.twoFactorVerificationWindowMinutes,
        );
        if (!verified) {
          return reply.code(403).send({ error: "two-factor verification required" });
        }
      }
    }

    const intent = await services.payments.createIntent({
      userId: principal.userId,
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
