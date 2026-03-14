import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";

type GuestChatBody = {
  model?: string;
  messages?: Array<{ role: string; content: string }>;
};

export function registerGuestChatRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: GuestChatBody }>("/v1/internal/chat/guest", async (request, reply) => {
    const configuredToken = process.env.WEB_INTERNAL_GUEST_TOKEN;
    const receivedToken = typeof request.headers["x-web-guest-token"] === "string"
      ? request.headers["x-web-guest-token"]
      : undefined;
    const guestId = typeof request.headers["x-guest-id"] === "string"
      ? request.headers["x-guest-id"].trim()
      : "";

    if (!configuredToken || !receivedToken || receivedToken !== configuredToken || !guestId) {
      return reply.code(403).send({ error: "forbidden" });
    }

    const forwardedGuestIp = typeof request.headers["x-guest-client-ip"] === "string"
      ? request.headers["x-guest-client-ip"].trim()
      : "";
    const guestIp = forwardedGuestIp || request.ip || "unknown";
    const guestKey = `guest:${guestId}:${guestIp}`;
    const allowed = services.rateLimiter?.allow ? await services.rateLimiter.allow(guestKey) : true;
    if (!allowed) {
      return reply.code(429).send({ error: "rate limit exceeded" });
    }

    const result = await services.ai.guestChatCompletions(
      guestId,
      request.body?.model,
      request.body?.messages ?? [],
      guestIp,
    );
    if ("error" in result) {
      return reply.code(result.statusCode).send({ error: result.error });
    }

    for (const [key, value] of Object.entries(result.headers ?? {})) {
      reply.header(key, value);
    }
    reply.code(result.statusCode);
    return result.body;
  });
}
