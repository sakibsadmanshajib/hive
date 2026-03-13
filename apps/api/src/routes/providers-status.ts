import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { hasValidAdminToken } from "./admin-auth";

/**
 * Sanitizes internal provider status for public consumption, hiding internal details
 * and mapping circuit breaker states to user-friendly availability strings.
 */
function sanitizeStatus(status: Awaited<ReturnType<RuntimeServices["ai"]["providersStatus"]>>) {
  return status.providers.map((provider) => {
    let state = provider.enabled ? (provider.healthy ? "ready" : "degraded") : "disabled";
    if (provider.circuit.state === "OPEN") {
      state = "circuit-open";
    }
    return {
      name: provider.name,
      enabled: provider.enabled,
      healthy: provider.healthy,
      state,
    };
  });
}

/**
 * Registers routes for checking provider health and circuit breaker status.
 *
 * GET /v1/providers/status - Publicly accessible sanitized health status.
 * GET /v1/providers/status/internal - Detailed diagnostics for operators (protected).
 */
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
    if (!hasValidAdminToken(request.headers, services.env.adminStatusToken)) {
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
