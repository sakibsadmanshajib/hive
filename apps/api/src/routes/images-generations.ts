import type { FastifyInstance } from "fastify";
import type { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import type { RuntimeServices } from "../runtime/services";
import { ImagesGenerationsBodySchema } from "../schemas/images-generations";
import { inferUsageChannel, requirePrincipal } from "./auth";
import { sendApiError } from "./api-error";

export function registerImagesGenerationsRoute(
  app: FastifyInstance<any, any, any, any, TypeBoxTypeProvider>,
  services: RuntimeServices,
): void {
  app.post("/v1/images/generations", {
    schema: { body: ImagesGenerationsBodySchema },
  }, async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "image",
      requiredSetting: "generateImage",
    });
    if (!principal) {
      return;
    }

    const allowed = await services.rateLimiter.allow(principal.userId);
    if (!allowed) {
      return sendApiError(reply, 429, "rate limit exceeded", { code: "rate_limit_exceeded" });
    }

    const prompt = request.body?.prompt?.trim();
    if (!prompt) {
      return sendApiError(reply, 400, "prompt is required", { param: "prompt" });
    }

    const result = await services.ai.imageGeneration(principal.userId, {
      model: request.body?.model,
      prompt,
      n: request.body?.n,
      size: request.body?.size,
      responseFormat: request.body?.response_format,
      user: request.body?.user,
    }, {
      channel: inferUsageChannel(request, principal),
      apiKeyId: principal.apiKeyId,
    });
    if ("error" in result) {
      if (result.headers) {
        for (const [key, value] of Object.entries(result.headers)) {
          reply.header(key, value);
        }
      }
      return sendApiError(reply, result.statusCode, result.error ?? "Unknown error");
    }

    for (const [key, value] of Object.entries(result.headers)) {
      reply.header(key, value);
    }
    reply.code(result.statusCode);
    return result.body;
  });
}
