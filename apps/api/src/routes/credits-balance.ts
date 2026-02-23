import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requireApiUser } from "./auth";

export function registerCreditsBalanceRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/credits/balance", async (request, reply) => {
    const userId = await requireApiUser(request, reply, services, "billing");
    if (!userId) {
      return;
    }

    const credits = await services.credits.getBalance(userId);
    return {
      user_id: userId,
      credits,
    };
  });
}
