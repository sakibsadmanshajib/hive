import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

type ImageBody = {
  prompt?: string;
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

    const result = await services.ai.imageGeneration(principal.userId, request.body?.prompt ?? "");
    if ("error" in result) {
      return reply.code(result.statusCode).send({ error: result.error });
    }

    reply.header("x-actual-credits", result.headers["x-actual-credits"]).code(result.statusCode);
    return result.body;
  });
}
