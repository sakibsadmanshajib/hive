import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { inferUsageChannel, requireApiPrincipal } from "./auth";

type ResponseBody = {
  input?: string;
};

export function registerResponsesRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: ResponseBody }>("/v1/responses", async (request, reply) => {
    const principal = await requireApiPrincipal(request, reply, services, "chat");
    if (!principal) {
      return;
    }

    const allowed = await services.rateLimiter.allow(principal.userId);
    if (!allowed) {
      return reply.code(429).send({ error: "rate limit exceeded" });
    }

    const result = await services.ai.responses(principal.userId, request.body?.input ?? "", {
      channel: inferUsageChannel(request, principal),
      apiKeyId: principal.apiKeyId,
    });
    if ("error" in result) {
      return reply.code(result.statusCode).send({ error: result.error });
    }

    reply.code(result.statusCode);
    return result.body;
  });
}
