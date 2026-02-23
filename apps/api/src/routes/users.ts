import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requireApiUser } from "./auth";

type RegisterBody = {
  email: string;
  password: string;
  name?: string;
};

type LoginBody = {
  email: string;
  password: string;
};

type CreateKeyBody = {
  scopes?: string[];
};

type RevokeKeyBody = {
  key: string;
};

export function registerUserRoutes(app: FastifyInstance, services: RuntimeServices): void {
  app.post<{ Body: RegisterBody }>("/v1/users/register", async (request, reply) => {
    const email = request.body?.email?.trim();
    const password = request.body?.password;
    if (!email || !password || password.length < 8) {
      return reply.code(400).send({ error: "email and password(min 8 chars) required" });
    }
    const result = await services.users.register({
      email,
      password,
      name: request.body?.name?.trim() || undefined,
    });
    if ("error" in result) {
      return reply.code(409).send({ error: result.error });
    }
    return reply.code(201).send({
      user: {
        user_id: result.userId,
        email: result.email,
        name: result.name,
      },
      api_key: result.apiKey,
    });
  });

  app.post<{ Body: LoginBody }>("/v1/users/login", async (request, reply) => {
    const email = request.body?.email?.trim();
    const password = request.body?.password;
    if (!email || !password) {
      return reply.code(400).send({ error: "email and password required" });
    }
    const result = await services.users.login({ email, password });
    if ("error" in result) {
      return reply.code(401).send({ error: result.error });
    }
    return {
      user: {
        user_id: result.userId,
        email: result.email,
        name: result.name,
      },
      api_key: result.apiKey,
    };
  });

  app.get("/v1/users/me", async (request, reply) => {
    const userId = await requireApiUser(request, reply, services, "usage");
    if (!userId) {
      return;
    }
    const me = await services.users.me(userId);
    if (!me) {
      return reply.code(404).send({ error: "user not found" });
    }
    const balance = await services.credits.getBalance(userId);
    return {
      user: {
        user_id: me.userId,
        email: me.email,
        name: me.name,
        created_at: me.createdAt,
      },
      api_keys: me.apiKeys,
      credits: balance,
    };
  });

  app.post<{ Body: CreateKeyBody }>("/v1/users/api-keys", async (request, reply) => {
    const userId = await requireApiUser(request, reply, services, "usage");
    if (!userId) {
      return;
    }
    const scopes = request.body?.scopes && request.body.scopes.length > 0 ? request.body.scopes : ["chat", "image", "usage", "billing"];
    const key = await services.users.createApiKey(userId, scopes);
    return reply.code(201).send({ key, scopes });
  });

  app.delete<{ Body: RevokeKeyBody }>("/v1/users/api-keys", async (request, reply) => {
    const userId = await requireApiUser(request, reply, services, "usage");
    if (!userId) {
      return;
    }
    const key = request.body?.key;
    if (!key) {
      return reply.code(400).send({ error: "key is required" });
    }
    const revoked = await services.users.revokeApiKey(userId, key);
    return { revoked };
  });
}
