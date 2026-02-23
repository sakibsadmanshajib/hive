import type { FastifyReply, FastifyRequest } from "fastify";
import type { RuntimeServices } from "../runtime/services";

const DEMO_KEY_PREFIX = "dev-user-";

export async function requireApiUser(
  request: FastifyRequest,
  reply: FastifyReply,
  services: RuntimeServices,
  requiredScope: "chat" | "image" | "usage" | "billing",
): Promise<string | undefined> {
  const apiKey = request.headers["x-api-key"];
  if (!apiKey || typeof apiKey !== "string") {
    reply.code(401).send({ error: "missing x-api-key" });
    return undefined;
  }

  if (services.env.allowDevApiKeyPrefix && apiKey.startsWith(DEMO_KEY_PREFIX)) {
    return `user-${apiKey.slice(DEMO_KEY_PREFIX.length)}`;
  }

  const userId = await services.users.validateApiKey(apiKey, requiredScope);
  if (!userId) {
    reply.code(401).send({ error: "invalid api key or scope" });
    return undefined;
  }

  return userId;
}
