import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

type ImageBody = {
  model?: string;
  prompt?: string;
  n?: number;
  size?: string;
  response_format?: "url" | "b64_json";
  user?: string;
};

export function registerImagesGenerationsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: ImageBody }>("/v1/images/generations", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "image",
      requiredSetting: "generateImage",
    });
    if (!principal) {
      return;
    }

    const allowed = await services.rateLimiter.allow(principal.userId);
    if (!allowed) {
      return reply.code(429).send({ error: "rate limit exceeded" });
    }

    const prompt = request.body?.prompt?.trim();
    if (!prompt) {
      return reply.code(400).send({ error: "prompt is required" });
    }

    const result = await services.ai.imageGeneration(principal.userId, {
      model: request.body?.model,
      prompt,
      n: request.body?.n,
      size: request.body?.size,
      responseFormat: request.body?.response_format,
      user: request.body?.user,
    });
    if ("error" in result) {
      if (result.headers) {
        for (const [key, value] of Object.entries(result.headers)) {
          reply.header(key, value);
        }
      }
      return reply.code(result.statusCode).send({ error: result.error });
    }

    for (const [key, value] of Object.entries(result.headers)) {
      reply.header(key, value);
    }
    reply.code(result.statusCode);
    return result.body;
  });
}
