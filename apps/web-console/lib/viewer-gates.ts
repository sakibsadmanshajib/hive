import { type Permission, PERMISSIONS } from "./control-plane/permissions.generated";

export type { Permission };
export { PERMISSIONS };

export interface ViewerWithPermissions {
  permissions: string[];
}

export function can(viewer: ViewerWithPermissions, perm: Permission): boolean {
  return new Set(viewer.permissions).has(perm);
}
