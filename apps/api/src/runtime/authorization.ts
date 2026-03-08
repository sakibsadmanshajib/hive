import type { SupabaseClient } from "@supabase/supabase-js";

export type PermissionKey =
  | "chat:write"
  | "image:generate"
  | "usage:read"
  | "billing:read"
  | "billing:write"
  | "users:manage_api_keys"
  | "users:settings:read"
  | "users:settings:write";

export type AuthorizationPrincipal = {
  userId: string;
  scopes?: string[];
  permissions?: string[];
};

const scopeBridge: Record<string, PermissionKey[]> = {
  chat: ["chat:write"],
  image: ["image:generate"],
  usage: ["usage:read", "users:settings:read"],
  billing: ["billing:read", "billing:write", "users:manage_api_keys"],
};

export class AuthorizationService {
  constructor(private readonly supabase: SupabaseClient) { }

  async hasPermission(principal: AuthorizationPrincipal, permission: PermissionKey): Promise<boolean> {
    const staticPermissions = new Set(principal.permissions ?? []);
    if (staticPermissions.has(permission)) {
      return true;
    }

    for (const scope of principal.scopes ?? []) {
      const mapped = scopeBridge[scope] ?? [];
      if (mapped.includes(permission)) {
        return true;
      }
    }

    const { data } = await this.supabase
      .from("user_roles")
      .select("role_permissions(permission_key)")
      .eq("user_id", principal.userId);

    const dbPermissions = (data ?? []).flatMap((row: any) =>
      Array.isArray(row.role_permissions)
        ? row.role_permissions.map((rp: any) => rp.permission_key)
        : [],
    );
    return dbPermissions.includes(permission);
  }

  async requirePermission(principal: AuthorizationPrincipal, permission: PermissionKey): Promise<boolean> {
    return this.hasPermission(principal, permission);
  }
}

export function mapScopeToPrimaryPermission(scope: "chat" | "image" | "usage" | "billing"): PermissionKey {
  switch (scope) {
    case "chat":
      return "chat:write";
    case "image":
      return "image:generate";
    case "usage":
      return "usage:read";
    case "billing":
      return "billing:read";
    default:
      return "usage:read";
  }
}
