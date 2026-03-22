import { Readable } from "node:stream";
import type { FastifyInstance } from "fastify";
import type { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import type { RuntimeServices } from "../runtime/services";
import { ChatCompletionsBodySchema } from "../schemas/chat-completions";
import { inferUsageChannel, requireV1ApiPrincipal } from "./auth";
import { sendApiError } from "./api-error";
import { setNoDispatchDiffHeaders } from "./diff-headers";

export function registerChatCompletionsRoute(
  app: FastifyInstance<any, any, any, any, TypeBoxTypeProvider>,
  services: RuntimeServices,
): void {
  app.post("/v1/chat/completions", {
    schema: { body: ChatCompletionsBodySchema },
  }, async (request, reply) => {
    setNoDispatchDiffHeaders(reply);

    const principal = await requireV1ApiPrincipal(request, reply, services, "chat");
    if (!principal) {
      return;
    }

    if (request.body?.stream === true) {
      const streamResult = await services.ai.chatCompletionsStream(
        principal.userId,
        request.body,
        {
          channel: inferUsageChannel(request, principal),
          apiKeyId: principal.apiKeyId,
        },
      );
      if ("error" in streamResult) {
        return sendApiError(reply, streamResult.statusCode, streamResult.error ?? "Unknown error");
      }

      reply
        .header("content-type", "text/event-stream")
        .header("cache-control", "no-cache")
        .header("connection", "keep-alive")
        .header("x-model-routed", streamResult.headers["x-model-routed"])
        .header("x-provider-used", streamResult.headers["x-provider-used"])
        .header("x-provider-model", streamResult.headers["x-provider-model"])
        .header("x-actual-credits", streamResult.headers["x-actual-credits"]);

      const nodeStream = Readable.fromWeb(streamResult.response.body as any);

      // Abort upstream connection if client disconnects
      request.raw.on("close", () => {
        if (!nodeStream.destroyed) {
          nodeStream.destroy();
        }
      });

      return reply.send(nodeStream);
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
