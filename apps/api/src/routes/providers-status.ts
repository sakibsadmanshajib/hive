import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";

function sanitizeStatus(status: Awaited<ReturnType<RuntimeServices["ai"]["providersStatus"]>>) {
  return status.providers.map((provider) => ({
    name: provider.name,
    enabled: provider.enabled,
    healthy: provider.healthy,
    state: provider.enabled ? (provider.healthy ? "ready" : "degraded") : "disabled",
  }));
}

export function registerProvidersStatusRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/providers/status", async () => {
    const status = await services.ai.providersStatus();
    const providers = sanitizeStatus(status);

    return {
      object: "providers.status",
      data: providers,
    };
  });

  app.get("/v1/providers/status/internal", async (request, reply) => {
    const expectedToken = services.env.adminStatusToken;
    const providedToken = request.headers["x-admin-token"];
    if (!expectedToken || typeof providedToken !== "string" || providedToken !== expectedToken) {
      reply.code(401);
      return { error: "unauthorized" };
    }

    const status = await services.ai.providersStatus();
    return {
      object: "providers.status.internal",
      data: status.providers,
    };
  });
}
