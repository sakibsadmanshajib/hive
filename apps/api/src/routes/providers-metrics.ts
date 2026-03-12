import type { FastifyInstance } from "fastify";
import type { ProviderMetricsSummary } from "../providers/types";
import type { RuntimeServices } from "../runtime/services";

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

function isAuthorized(requestHeaders: Record<string, unknown> | undefined, expectedToken: string | undefined) {
  const providedToken = requestHeaders?.["x-admin-token"];
  return expectedToken && typeof providedToken === "string" && providedToken === expectedToken;
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
    if (!isAuthorized(request.headers, services.env.adminStatusToken)) {
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
    if (!isAuthorized(request.headers, services.env.adminStatusToken)) {
      reply.code(401);
      return { error: "unauthorized" };
    }

    const metrics = await services.ai.providersMetricsPrometheus();
    reply.header("content-type", metrics.contentType);
    return metrics.body;
  });
}
