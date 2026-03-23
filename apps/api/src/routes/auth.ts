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

async function enforceRequirements(
  principal: AuthPrincipal,
  reply: FastifyReply,
  services: RuntimeServices,
  requirements: AuthRequirements,
): Promise<AuthPrincipal | undefined> {
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

  return enforceRequirements(principal, reply, services, requirements);
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

export async function requireV1ApiPrincipal(
  request: FastifyRequest,
  reply: FastifyReply,
  services: RuntimeServices,
  requiredScope?: "chat" | "image" | "usage" | "billing",
): Promise<AuthPrincipal | undefined> {
  void requiredScope;

  const bearerToken = readBearerToken(request);
  if (!bearerToken) {
    sendApiError(reply, 401, "No API key provided", { code: "invalid_api_key" });
    return undefined;
  }

  const resolved = await services.users.resolveApiKey(bearerToken);
  if (!resolved) {
    sendApiError(reply, 401, "Incorrect API key provided", { code: "invalid_api_key" });
    return undefined;
  }

  const principal: AuthPrincipal = {
    userId: resolved.userId,
    authType: "apiKey",
    scopes: resolved.scopes,
    apiKeyId: resolved.apiKeyId,
  };

  const allowed = await enforceRequirements(principal, reply, services, { requiredScope });
  if (!allowed) {
    return undefined;
  }

  const requiredSettings =
    requiredScope === "image"
      ? (["apiEnabled", "generateImage"] as const)
      : requiredScope
        ? (["apiEnabled"] as const)
        : ([] as const);
  if (requiredSettings.length === 0) {
    return principal;
  }

  const settings = await services.userSettings.getForUser(principal.userId);
  for (const requiredSetting of requiredSettings) {
    if (!services.userSettings.canUse(requiredSetting, settings)) {
      sendApiError(reply, 403, `setting disabled: ${requiredSetting}`);
      return undefined;
    }
  }

  return principal;
}

export function inferUsageChannel(request: FastifyRequest, principal: AuthPrincipal): UsageChannel {
  if (principal.authType !== "session") {
    return "api";
  }

  return hasTrustedBrowserOrigin(request) ? "web" : "api";
}
