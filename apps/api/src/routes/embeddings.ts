import type { FastifyInstance } from "fastify";
import type { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import type { RuntimeServices } from "../runtime/services";
import { EmbeddingsBodySchema } from "../schemas/embeddings";
import { inferUsageChannel, requireV1ApiPrincipal } from "./auth";
import { sendApiError } from "./api-error";
import { setNoDispatchDiffHeaders } from "./diff-headers";

export function registerEmbeddingsRoute(
  app: FastifyInstance<any, any, any, any, TypeBoxTypeProvider>,
  services: RuntimeServices,
): void {
  app.post("/v1/embeddings", {
    schema: { body: EmbeddingsBodySchema },
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

    const result = await services.ai.embeddings(
      principal.userId,
      request.body as {
        model: string;
        input: string | string[];
        encoding_format?: "float" | "base64";
        dimensions?: number;
        user?: string;
      },
      {
        channel: inferUsageChannel(request, principal),
        apiKeyId: principal.apiKeyId,
      },
    );
    if ("error" in result) {
      return sendApiError(reply, result.statusCode, result.error ?? "Unknown error");
    }

    for (const [key, value] of Object.entries(result.headers)) {
      reply.header(key, value);
    }
    reply.code(result.statusCode);
    return result.body;
  });
}
