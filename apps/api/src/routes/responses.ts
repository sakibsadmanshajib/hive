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

    if (result.headers) {
      reply
        .header("x-model-routed", result.headers["x-model-routed"])
        .header("x-provider-used", result.headers["x-provider-used"])
        .header("x-provider-model", result.headers["x-provider-model"])
        .header("x-actual-credits", result.headers["x-actual-credits"]);
    }
    reply.code(result.statusCode);
    return result.body;
  });
}
