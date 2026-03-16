import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";

function hasTrustedGuestToken(headers: Record<string, unknown>): boolean {
  const configuredToken = process.env.WEB_INTERNAL_GUEST_TOKEN;
  const receivedToken = typeof headers["x-web-guest-token"] === "string" ? headers["x-web-guest-token"] : undefined;
  return Boolean(configuredToken && receivedToken && receivedToken === configuredToken);
}

function readGuestId(headers: Record<string, unknown>): string {
  return typeof headers["x-guest-id"] === "string" ? headers["x-guest-id"].trim() : "";
}

function readCreateSessionBody(body: unknown): { title?: string } | null {
  if (body === null || body === undefined) {
    return {};
  }
  if (typeof body !== "object") {
    return null;
  }
  const record = body as Record<string, unknown>;
  if (record.title !== undefined && typeof record.title !== "string") {
    return null;
  }
  return {
    title: typeof record.title === "string" ? record.title.trim() : undefined,
  };
}

function readSendMessageBody(body: unknown): { model?: string; content: string } | null {
  if (!body || typeof body !== "object") {
    return null;
  }
  const record = body as Record<string, unknown>;
  if (typeof record.content !== "string" || !record.content.trim()) {
    return null;
  }
  if (record.model !== undefined && typeof record.model !== "string") {
    return null;
  }
  return {
    model: typeof record.model === "string" ? record.model : undefined,
    content: record.content.trim(),
  };
}

export function registerGuestChatSessionsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/internal/guest/chat/sessions", async (request, reply) => {
    if (!hasTrustedGuestToken(request.headers as Record<string, unknown>)) {
      return reply.code(403).send({ error: "forbidden" });
    }

    const guestId = readGuestId(request.headers as Record<string, unknown>);
    if (!guestId) {
      return reply.code(400).send({ error: "missing guest id" });
    }

    const sessions = await services.chatHistory.listSessionsForGuest(guestId);
    request.log?.debug({ guestId, sessionCount: sessions.length, sessionIds: sessions.map((s) => s.id) }, "guest chat list");
    return {
      object: "list",
      data: sessions,
    };
  });

  app.post("/v1/internal/guest/chat/sessions", async (request, reply) => {
    if (!hasTrustedGuestToken(request.headers as Record<string, unknown>)) {
      return reply.code(403).send({ error: "forbidden" });
    }

    const guestId = readGuestId(request.headers as Record<string, unknown>);
    if (!guestId) {
      return reply.code(400).send({ error: "missing guest id" });
    }

    const input = readCreateSessionBody(request.body);
    if (!input) {
      return reply.code(400).send({ error: "invalid chat session create request" });
    }

    const session = await services.chatHistory.createSessionForGuest(guestId, input);
    return reply.code(201).send(session);
  });

  app.get<{ Params: { sessionId: string } }>("/v1/internal/guest/chat/sessions/:sessionId", async (request, reply) => {
    if (!hasTrustedGuestToken(request.headers as Record<string, unknown>)) {
      return reply.code(403).send({ error: "forbidden" });
    }

    const guestId = readGuestId(request.headers as Record<string, unknown>);
    if (!guestId) {
      return reply.code(400).send({ error: "missing guest id" });
    }

    const sessionId = request.params?.sessionId?.trim();
    if (!sessionId) {
      return reply.code(400).send({ error: "missing chat session id" });
    }

    const session = await services.chatHistory.getSessionForGuest(guestId, sessionId);
    if (!session) {
      request.log?.debug({ guestId, sessionId }, "guest chat get not found");
      return reply.code(404).send({ error: "chat session not found" });
    }
    request.log?.debug({ guestId, sessionId }, "guest chat get ok");
    return session;
  });

  app.post<{ Params: { sessionId: string } }>(
    "/v1/internal/guest/chat/sessions/:sessionId/messages",
    async (request, reply) => {
      if (!hasTrustedGuestToken(request.headers as Record<string, unknown>)) {
        return reply.code(403).send({ error: "forbidden" });
      }

      const guestId = readGuestId(request.headers as Record<string, unknown>);
      if (!guestId) {
        return reply.code(400).send({ error: "missing guest id" });
      }

      const sessionId = request.params?.sessionId?.trim();
      if (!sessionId) {
        return reply.code(400).send({ error: "missing chat session id" });
      }

      const input = readSendMessageBody(request.body);
      if (!input) {
        return reply.code(400).send({ error: "invalid chat message request" });
      }

      const guestIp = typeof request.headers["x-forwarded-for"] === "string"
        ? request.headers["x-forwarded-for"].split(",")[0]?.trim()
        : undefined;

      const result = await services.chatHistory.sendMessageForGuest(
        guestId,
        sessionId,
        input,
        guestIp,
      );
      if (result.type === "not_found") {
        return reply.code(404).send({ error: "chat session not found" });
      }
      if (result.type === "error") {
        return reply.code(result.statusCode).send({ error: result.error });
      }

      return result.session;
    },
  );
}
