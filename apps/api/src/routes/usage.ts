import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requireApiUser } from "./auth";

export function registerUsageRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/usage", async (request, reply) => {
    const userId = await requireApiUser(request, reply, services, "usage");
    if (!userId) {
      return;
    }

    const data = await services.usage.list(userId);
    return {
      object: "list",
      data,
    };
  });
}
