import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

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

async function requireSessionChatPrincipal(
  request: FastifyRequest,
  reply: FastifyReply,
  services: RuntimeServices,
) {
  const principal = await requirePrincipal(request, reply, services, {
    requiredPermission: "chat:write",
  });
  if (!principal) {
    return undefined;
  }
  if (principal.authType !== "session") {
    reply.code(403).send({ error: "forbidden" });
    return undefined;
  }
  return principal;
}

export function registerChatSessionsRoute(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/chat/sessions", async (request, reply) => {
    const principal = await requireSessionChatPrincipal(request, reply, services);
    if (!principal) {
      return;
    }

    const sessions = await services.chatHistory.listSessions(principal.userId);
    return {
      object: "list",
      data: sessions,
    };
  });

  app.post("/v1/chat/sessions", async (request, reply) => {
    const principal = await requireSessionChatPrincipal(request, reply, services);
    if (!principal) {
      return;
    }

    const input = readCreateSessionBody(request.body);
    if (!input) {
      return reply.code(400).send({ error: "invalid chat session create request" });
    }

    const session = await services.chatHistory.createSession(principal.userId, input);
    return reply.code(201).send(session);
  });

  app.get<{ Params: { sessionId: string } }>("/v1/chat/sessions/:sessionId", async (request, reply) => {
    const principal = await requireSessionChatPrincipal(request, reply, services);
    if (!principal) {
      return;
    }

    const sessionId = request.params?.sessionId?.trim();
    if (!sessionId) {
      return reply.code(400).send({ error: "missing chat session id" });
    }

    const session = await services.chatHistory.getSession(principal.userId, sessionId);
    if (!session) {
      return reply.code(404).send({ error: "chat session not found" });
    }

    return session;
  });

  app.post<{ Params: { sessionId: string } }>("/v1/chat/sessions/:sessionId/messages", async (request, reply) => {
    const principal = await requireSessionChatPrincipal(request, reply, services);
    if (!principal) {
      return;
    }

    const sessionId = request.params?.sessionId?.trim();
    if (!sessionId) {
      return reply.code(400).send({ error: "missing chat session id" });
    }

    const input = readSendMessageBody(request.body);
    if (!input) {
      return reply.code(400).send({ error: "invalid chat message request" });
    }

    const result = await services.chatHistory.sendMessage(principal.userId, sessionId, input);
    if (result.type === "not_found") {
      return reply.code(404).send({ error: "chat session not found" });
    }
    if (result.type === "error") {
      return reply.code(result.statusCode).send({ error: result.error });
    }

    return result.session;
  });
}
