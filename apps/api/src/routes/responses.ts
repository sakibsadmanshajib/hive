import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { inferUsageChannel, requireApiPrincipal } from "./auth";
import { sendApiError } from "./api-error";

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
      return sendApiError(reply, 429, "rate limit exceeded", { code: "rate_limit_exceeded" });
    }

    const result = await services.ai.responses(principal.userId, request.body?.input ?? "", {
      channel: inferUsageChannel(request, principal),
      apiKeyId: principal.apiKeyId,
    });
    if ("error" in result) {
      return sendApiError(reply, result.statusCode, result.error);
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
