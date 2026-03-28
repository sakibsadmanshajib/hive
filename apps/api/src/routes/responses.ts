import type { FastifyInstance } from "fastify";
import type { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import type { RuntimeServices } from "../runtime/services";
import { ResponsesBodySchema } from "../schemas/responses";
import { inferUsageChannel, requireV1ApiPrincipal } from "./auth";
import { sendApiError } from "./api-error";
import { setNoDispatchDiffHeaders } from "./diff-headers";

export function registerResponsesRoute(
  app: FastifyInstance<any, any, any, any, TypeBoxTypeProvider>,
  services: RuntimeServices,
): void {
  app.post("/v1/responses", {
    schema: { body: ResponsesBodySchema },
  }, async (request, reply) => {
    setNoDispatchDiffHeaders(reply);

    const principal = await requireV1ApiPrincipal(request, reply, services, "chat");
    if (!principal) {
      return;
    }

    const allowed = await services.rateLimiter.allow(principal.userId);
    if (!allowed) {
      return sendApiError(reply, 429, "rate limit exceeded", { code: "rate_limit_exceeded" });
    }

    const result = await services.ai.responses(principal.userId, request.body, {
      channel: inferUsageChannel(request, principal),
      apiKeyId: principal.apiKeyId,
    });
    if ("error" in result) {
      return sendApiError(reply, result.statusCode, result.error ?? "Unknown error");
    }

    if (result.headers) {
      for (const [key, value] of Object.entries(result.headers)) {
        reply.header(key, value);
      }
    }
    reply.code(result.statusCode);
    return result.body;
  });
}
