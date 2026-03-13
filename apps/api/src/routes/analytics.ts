import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { hasValidAdminToken } from "./admin-auth";

export function registerAnalyticsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/analytics/internal", async (request, reply) => {
    if (!hasValidAdminToken(request.headers, services.env.adminStatusToken)) {
      reply.code(401);
      return { error: "unauthorized" };
    }

    return {
      object: "analytics.traffic",
      data: await services.usage.trafficAnalytics(),
    };
  });
}
