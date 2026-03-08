import type { FastifyReply, FastifyRequest } from "fastify";
import type { RuntimeServices } from "../runtime/services";
import { mapScopeToPrimaryPermission, type PermissionKey } from "../runtime/authorization";
import type { UserSettingKey } from "../runtime/user-settings";

const DEMO_KEY_PREFIX = "dev-user-";

export type AuthPrincipal = {
  userId: string;
  authType: "apiKey" | "session";
  scopes: string[];
};

type AuthRequirements = {
  requiredScope?: "chat" | "image" | "usage" | "billing";
  requiredPermission?: PermissionKey;
  requiredSetting?: UserSettingKey;
};

function readBearerToken(request: FastifyRequest): string | null {
  const authHeader = request.headers.authorization;
  if (!authHeader || typeof authHeader !== "string") {
    return null;
  }
  const [scheme, token] = authHeader.split(" ");
  if (scheme?.toLowerCase() !== "bearer" || !token) {
    return null;
  }
  return token;
}

async function resolvePrincipal(
  request: FastifyRequest,
  services: RuntimeServices,
  requiredScope?: "chat" | "image" | "usage" | "billing",
): Promise<AuthPrincipal | null> {
  const bearerToken = readBearerToken(request);
  if (bearerToken) {
    const supabaseAuthEnabled = services.env.supabase?.flags.authEnabled ?? false;
    const { data: { user }, error } = await services.supabaseAuth.getSessionPrincipal(bearerToken).then(p => ({ data: { user: p ? { id: p.userId } : null }, error: null }));
    const session = services.env.supabase.flags.authEnabled
      ? user ? { userId: user.id } : null
      : null;
    if (session) {
      return {
        userId: session.userId,
        authType: "session",
        scopes: requiredScope ? [requiredScope] : ["chat", "image", "usage", "billing"],
      };
    }
  }

  const apiKey = request.headers["x-api-key"];
  if (!apiKey || typeof apiKey !== "string") {
    return null;
  }

  if (services.env.allowDevApiKeyPrefix && apiKey.startsWith(DEMO_KEY_PREFIX)) {
    return {
      userId: `user-${apiKey.slice(DEMO_KEY_PREFIX.length)}`,
      authType: "apiKey",
      scopes: requiredScope ? [requiredScope] : ["chat", "image", "usage", "billing"],
    };
  }

  const resolved = await services.users.resolveApiKey(apiKey);
  if (!resolved) {
    return null;
  }

  return {
    userId: resolved.userId,
    authType: "apiKey",
    scopes: resolved.scopes,
  };
}

export async function requirePrincipal(
  request: FastifyRequest,
  reply: FastifyReply,
  services: RuntimeServices,
  requirements: AuthRequirements,
): Promise<AuthPrincipal | undefined> {
  const principal = await resolvePrincipal(request, services, requirements.requiredScope);
  if (!principal) {
    reply.code(401).send({ error: "missing or invalid credentials" });
    return undefined;
  }

  const permission = requirements.requiredPermission
    ?? (requirements.requiredScope ? mapScopeToPrimaryPermission(requirements.requiredScope) : undefined);
  if (permission) {
    const allowed = await services.authz.requirePermission(principal, permission);
    if (!allowed) {
      reply.code(403).send({ error: "forbidden" });
      return undefined;
    }
  }

  if (requirements.requiredSetting) {
    const settings = await services.userSettings.getForUser(principal.userId);
    if (!services.userSettings.canUse(requirements.requiredSetting, settings)) {
      reply.code(403).send({ error: `setting disabled: ${requirements.requiredSetting}` });
      return undefined;
    }
  }

  return principal;
}

export async function requireApiUser(
  request: FastifyRequest,
  reply: FastifyReply,
  services: RuntimeServices,
  requiredScope: "chat" | "image" | "usage" | "billing",
): Promise<string | undefined> {
  const principal = await requirePrincipal(request, reply, services, {
    requiredScope,
    requiredSetting: "apiEnabled",
  });
  if (!principal) {
    return undefined;
  }
  return principal.userId;
}
