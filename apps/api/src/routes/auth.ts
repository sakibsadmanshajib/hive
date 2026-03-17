import type { FastifyReply, FastifyRequest } from "fastify";
import type { UsageChannel } from "../domain/types";
import type { RuntimeServices } from "../runtime/services";
import { isAllowedBrowserOrigin } from "../runtime/cors-origins";
import { mapScopeToPrimaryPermission, type PermissionKey } from "../runtime/authorization";
import type { UserSettingKey } from "../runtime/user-settings";
import { sendApiError } from "./api-error";

const DEMO_KEY_PREFIX = "dev-user-";

export type AuthPrincipal = {
  userId: string;
  authType: "apiKey" | "session";
  scopes: string[];
  apiKeyId?: string;
};

type AuthRequirements = {
  requiredScope?: "chat" | "image" | "usage" | "billing";
  requiredPermission?: PermissionKey;
  requiredSetting?: UserSettingKey;
};

function readSingleHeaderValue(header: string | string[] | undefined): string | undefined {
  if (typeof header === "string") {
    return header;
  }
  if (Array.isArray(header)) {
    return header[0];
  }
  return undefined;
}

function hasTrustedBrowserOrigin(request: FastifyRequest): boolean {
  const origin = readSingleHeaderValue(request.headers.origin);
  if (origin && isAllowedBrowserOrigin(origin)) {
    return true;
  }

  const referer = readSingleHeaderValue(request.headers.referer);
  if (!referer) {
    return false;
  }

  try {
    return isAllowedBrowserOrigin(new URL(referer).origin);
  } catch {
    return false;
  }
}

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
  if (bearerToken && services.env.supabase.flags.authEnabled) {
    const principal = await services.supabaseAuth.getSessionPrincipal(bearerToken);
    if (principal) {
      await services.users.ensureSessionUser?.(principal);
      return {
        userId: principal.userId,
        authType: "session",
        scopes: requiredScope ? [requiredScope] : ["chat", "image", "usage", "billing"],
      };
    }
  }

  const apiKey = typeof request.headers["x-api-key"] === "string" ? request.headers["x-api-key"] : bearerToken;
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
    apiKeyId: resolved.apiKeyId,
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
    sendApiError(reply, 401, "missing or invalid credentials", { code: "invalid_api_key" });
    return undefined;
  }

  const permission = requirements.requiredPermission
    ?? (requirements.requiredScope ? mapScopeToPrimaryPermission(requirements.requiredScope) : undefined);
  if (permission) {
    const allowed = await services.authz.requirePermission(principal, permission);
    if (!allowed) {
      sendApiError(reply, 403, "forbidden");
      return undefined;
    }
  }

  if (requirements.requiredSetting) {
    const settings = await services.userSettings.getForUser(principal.userId);
    if (!services.userSettings.canUse(requirements.requiredSetting, settings)) {
      sendApiError(reply, 403, `setting disabled: ${requirements.requiredSetting}`);
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

export async function requireApiPrincipal(
  request: FastifyRequest,
  reply: FastifyReply,
  services: RuntimeServices,
  requiredScope: "chat" | "image" | "usage" | "billing",
): Promise<AuthPrincipal | undefined> {
  return requirePrincipal(request, reply, services, {
    requiredScope,
    requiredSetting: "apiEnabled",
  });
}

export function inferUsageChannel(request: FastifyRequest, principal: AuthPrincipal): UsageChannel {
  if (principal.authType !== "session") {
    return "api";
  }

  return hasTrustedBrowserOrigin(request) ? "web" : "api";
}
