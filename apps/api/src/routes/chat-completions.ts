import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requireApiUser } from "./auth";

type ChatBody = {
  model?: string;
  messages?: Array<{ role: string; content: string }>;
};

export function registerChatCompletionsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: ChatBody }>("/v1/chat/completions", async (request, reply) => {
    const userId = await requireApiUser(request, reply, services, "chat");
    if (!userId) {
      return;
    }

    const allowed = await services.rateLimiter.allow(userId);
    if (!allowed) {
      return reply.code(429).send({ error: "rate limit exceeded" });
    }

    const result = await services.ai.chatCompletions(userId, request.body?.model, request.body?.messages ?? []);
    if ("error" in result) {
      return reply.code(result.statusCode).send({ error: result.error });
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
