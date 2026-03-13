import type { FastifyInstance } from "fastify";
import type { ProviderMetricsSummary } from "../providers/types";
import type { RuntimeServices } from "../runtime/services";
import { hasValidAdminToken } from "./admin-auth";

function sanitizeMetrics(provider: ProviderMetricsSummary) {
  return {
    name: provider.name,
    enabled: provider.enabled,
    healthy: provider.healthy,
    circuitState: provider.circuit.state.toLowerCase(),
    requests: provider.requests,
    errors: provider.errors,
    errorRate: provider.errorRate,
    latencyMs: provider.latencyMs,
  };
}

export function registerProvidersMetricsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/providers/metrics", async () => {
    const metrics = await services.ai.providersMetrics();
    return {
      object: "providers.metrics",
      scrapedAt: metrics.scrapedAt,
      data: metrics.providers.map(sanitizeMetrics),
    };
  });

  app.get("/v1/providers/metrics/internal", async (request, reply) => {
    if (!hasValidAdminToken(request.headers, services.env.adminStatusToken)) {
      reply.code(401);
      return { error: "unauthorized" };
    }

    const metrics = await services.ai.providersMetrics();
    return {
      object: "providers.metrics.internal",
      scrapedAt: metrics.scrapedAt,
      data: metrics.providers,
    };
  });

  app.get("/v1/providers/metrics/internal/prometheus", async (request, reply) => {
    if (!hasValidAdminToken(request.headers, services.env.adminStatusToken)) {
      reply.code(401);
      return { error: "unauthorized" };
    }

    const metrics = await services.ai.providersMetricsPrometheus();
    reply.header("content-type", metrics.contentType);
    return metrics.body;
  });
}
