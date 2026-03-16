import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

function hasTrustedGuestToken(headers: Record<string, unknown>): boolean {
  const configuredToken = process.env.WEB_INTERNAL_GUEST_TOKEN;
  const receivedToken = typeof headers["x-web-guest-token"] === "string" ? headers["x-web-guest-token"] : undefined;
  return Boolean(configuredToken && receivedToken && receivedToken === configuredToken);
}

function readGuestId(headers: Record<string, unknown>): string {
  return typeof headers["x-guest-id"] === "string" ? headers["x-guest-id"].trim() : "";
}

type GuestSessionBody = {
  guestId?: string;
  expiresAt?: string;
  lastSeenIp?: string;
};

export function registerGuestAttributionRoutes(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: GuestSessionBody }>("/v1/internal/guest/session", async (request, reply) => {
    if (!hasTrustedGuestToken(request.headers as Record<string, unknown>)) {
      return reply.code(403).send({ error: "forbidden" });
    }

    const guestId = typeof request.body?.guestId === "string" ? request.body.guestId.trim() : "";
    const expiresAt = typeof request.body?.expiresAt === "string" ? request.body.expiresAt : "";
    if (!guestId || !expiresAt || Number.isNaN(Date.parse(expiresAt))) {
      return reply.code(400).send({ error: "invalid guest session" });
    }

    await services.guests.upsertSession({
      guestId,
      expiresAt,
      lastSeenIp: typeof request.body?.lastSeenIp === "string" ? request.body.lastSeenIp : undefined,
    });
    return reply.code(201).send({ guestId, persisted: true });
  });

  app.post("/v1/internal/guest/link", async (request, reply) => {
    if (!hasTrustedGuestToken(request.headers as Record<string, unknown>)) {
      return reply.code(403).send({ error: "forbidden" });
    }

    const guestId = readGuestId(request.headers as Record<string, unknown>);
    if (!guestId) {
      return reply.code(400).send({ error: "missing guest id" });
    }

    const principal = await requirePrincipal(request, reply, services, {});
    if (!principal) {
      return;
    }

    await services.users.linkGuest(guestId, principal.userId, "auth_session");
    await services.chatHistory.claimGuestSessionsForUser(guestId, principal.userId);
    return { guestId, linked: true, userId: principal.userId };
  });
}
