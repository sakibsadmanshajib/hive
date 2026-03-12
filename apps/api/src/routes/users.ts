import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requirePrincipal } from "./auth";

const ALLOWED_SCOPES = new Set(["chat", "image", "usage", "billing"]);

function readCreateApiKeyBody(body: unknown): { nickname: string; scopes: string[]; expiresAt?: string } | null {
  if (!body || typeof body !== "object") {
    return null;
  }
  const record = body as Record<string, unknown>;
  if (typeof record.nickname !== "string" || !record.nickname.trim()) {
    return null;
  }
  if (
    !Array.isArray(record.scopes)
    || !record.scopes.every((scope) => typeof scope === "string" && ALLOWED_SCOPES.has(scope))
  ) {
    return null;
  }
  if (record.expiresAt !== undefined && typeof record.expiresAt !== "string") {
    return null;
  }
  if (typeof record.expiresAt === "string") {
    const expiresAtMs = Date.parse(record.expiresAt);
    if (Number.isNaN(expiresAtMs) || expiresAtMs <= Date.now()) {
      return null;
    }
  }
  return {
    nickname: record.nickname.trim(),
    scopes: record.scopes as string[],
    expiresAt: record.expiresAt as string | undefined,
  };
}

export function registerUserRoutes(app: FastifyInstance, services: RuntimeServices): void {
  app.get("/v1/users/me", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredPermission: "users:manage_api_keys",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    const me = await services.users.me(principal.userId);
    if (!me) {
      reply.code(404).send({ error: "user not found" });
      return;
    }

    return {
      user: {
        user_id: me.userId,
        email: me.email,
        name: me.name,
        createdAt: me.createdAt,
      },
      credits: await services.credits.getBalance(principal.userId),
      api_keys: me.apiKeys,
      api_key_events: me.apiKeyEvents,
    };
  });

  app.get("/v1/users/api-keys", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredPermission: "users:manage_api_keys",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    const me = await services.users.me(principal.userId);
    if (!me) {
      reply.code(404).send({ error: "user not found" });
      return;
    }

    return {
      data: me.apiKeys,
      events: me.apiKeyEvents,
    };
  });

  app.post("/v1/users/api-keys", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredPermission: "users:manage_api_keys",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    const input = readCreateApiKeyBody(request.body);
    if (!input) {
      reply.code(400).send({ error: "invalid api key create request" });
      return;
    }

    const created = await services.users.createApiKey(principal.userId, input);
    return reply.code(201).send(created);
  });

  app.post("/v1/users/api-keys/:id/revoke", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredPermission: "users:manage_api_keys",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    const params = (request.params ?? {}) as { id?: string };
    if (!params.id) {
      reply.code(400).send({ error: "missing api key id" });
      return;
    }

    const revoked = await services.users.revokeApiKey(principal.userId, params.id);
    if (!revoked) {
      reply.code(404).send({ error: "api key not found" });
      return;
    }

    return { revoked: true, id: params.id };
  });
}
