import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { requireApiUser, requirePrincipal } from "./auth";
import { USER_SETTING_KEYS, type UserSettingKey, type UserSettings } from "../runtime/user-settings";

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

type UpdateSettingsBody = Partial<UserSettings>;

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
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "usage",
      requiredPermission: "users:manage_api_keys",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }
    if (services.env.auth.enforceTwoFactorSensitiveActions) {
      const settings = await services.userSettings.getForUser(principal.userId);
      if (settings.twoFactorEnabled) {
        const challengeId = request.headers["x-2fa-challenge-id"];
        if (typeof challengeId !== "string") {
          return reply.code(403).send({ error: "two-factor verification required" });
        }
        const verified = await services.twoFactor.hasRecentVerification(
          principal.userId,
          challengeId,
          services.env.auth.twoFactorVerificationWindowMinutes,
        );
        if (!verified) {
          return reply.code(403).send({ error: "two-factor verification required" });
        }
      }
    }
    const scopes = request.body?.scopes && request.body.scopes.length > 0 ? request.body.scopes : ["chat", "image", "usage", "billing"];
    const key = await services.users.createApiKey(principal.userId, scopes);
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

  app.get("/v1/users/settings", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "usage",
      requiredPermission: "users:settings:read",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }
    const settings = await services.userSettings.getForUser(principal.userId);
    return { user_id: principal.userId, settings };
  });

  app.patch<{ Body: UpdateSettingsBody }>("/v1/users/settings", async (request, reply) => {
    const principal = await requirePrincipal(request, reply, services, {
      requiredScope: "usage",
      requiredPermission: "users:settings:write",
      requiredSetting: "apiEnabled",
    });
    if (!principal) {
      return;
    }

    const body = request.body ?? {};
    const bodyKeys = Object.keys(body);
    const allowed = new Set<string>(USER_SETTING_KEYS);
    const invalidKeys = bodyKeys.filter((key) => !allowed.has(key));
    if (invalidKeys.length > 0) {
      return reply.code(400).send({ error: "invalid setting keys", invalid_keys: invalidKeys });
    }

    const patch = bodyKeys.reduce<Partial<UserSettings>>((acc, key) => {
      const value = (body as Record<string, unknown>)[key];
      if (typeof value === "boolean") {
        acc[key as UserSettingKey] = value;
      }
      return acc;
    }, {});

    const settings = await services.userSettings.updateForUser(principal.userId, patch);
    return { user_id: principal.userId, settings };
  });
}
