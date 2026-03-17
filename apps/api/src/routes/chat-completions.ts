import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { inferUsageChannel, requireApiPrincipal } from "./auth";
import { sendApiError } from "./api-error";

type ChatBody = {
  model?: string;
  messages?: Array<{ role: string; content: string }>;
};

export function registerChatCompletionsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: ChatBody }>("/v1/chat/completions", async (request, reply) => {
    const principal = await requireApiPrincipal(request, reply, services, "chat");
    if (!principal) {
      return;
    }

    const allowed = await services.rateLimiter.allow(principal.userId);
    if (!allowed) {
      return sendApiError(reply, 429, "rate limit exceeded", { code: "rate_limit_exceeded" });
    }

    const result = await services.ai.chatCompletions(
      principal.userId,
      request.body?.model,
      request.body?.messages ?? [],
      {
        channel: inferUsageChannel(request, principal),
        apiKeyId: principal.apiKeyId,
      },
    );
    if ("error" in result) {
      return sendApiError(reply, result.statusCode, result.error);
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
