import type { FastifyInstance } from "fastify";
import type { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import type { RuntimeServices } from "../runtime/services";
import { ChatCompletionsBodySchema } from "../schemas/chat-completions";
import { inferUsageChannel, requireV1ApiPrincipal } from "./auth";
import { sendApiError } from "./api-error";

export function registerChatCompletionsRoute(
  app: FastifyInstance<any, any, any, any, TypeBoxTypeProvider>,
  services: RuntimeServices,
): void {
  app.post("/v1/chat/completions", {
    schema: { body: ChatCompletionsBodySchema },
  }, async (request, reply) => {
    const principal = await requireV1ApiPrincipal(request, reply, services, "chat");
    if (!principal) {
      return;
    }

    if (request.body?.stream === true) {
      return sendApiError(reply, 400,
        "Streaming is not yet supported. Set stream: false or omit the stream parameter.",
        { code: "unsupported_parameter" },
      );
    }

    const allowed = await services.rateLimiter.allow(principal.userId);
    if (!allowed) {
      return sendApiError(reply, 429, "rate limit exceeded", { code: "rate_limit_exceeded" });
    }

    const result = await services.ai.chatCompletions(
      principal.userId,
      request.body,
      {
        channel: inferUsageChannel(request, principal),
        apiKeyId: principal.apiKeyId,
      },
    );
    if ("error" in result) {
      return sendApiError(reply, result.statusCode, result.error ?? "Unknown error");
    }

    reply
      .header("x-model-routed", result.headers["x-model-routed"])
      .header("x-provider-used", result.headers["x-provider-used"])
      .header("x-provider-model", result.headers["x-provider-model"])
      .header("x-actual-credits", result.headers["x-actual-credits"])
      .code(result.statusCode);

    return result.body;
  });
}
