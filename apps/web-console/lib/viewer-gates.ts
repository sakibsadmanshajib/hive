import { type Permission, PERMISSIONS } from "./control-plane/permissions.generated";

export type { Permission };
export { PERMISSIONS };

export interface ViewerWithPermissions {
  permissions: string[];
}

export function can(viewer: ViewerWithPermissions, perm: Permission): boolean {
  return new Set(viewer.permissions).has(perm);
}

// Role predicates for the console's operator surfaces (#947/#948/#949
// family): a hidden nav entry is not access control, so each operator page
// must refuse to render server-side for a caller the control-plane would 403.
// The control-plane stays the authority on every data endpoint; these
// predicates only decide whether the page shell may exist for this caller.

// Shape the predicates need from lib/control-plane/client's Viewer. Kept
// structural so tests can pass minimal fixtures without casts.
export interface RoleGateViewer {
  permissions: string[];
  current_account: { role: string };
}

// Providers is mounted behind control-plane RequirePlatformAdmin
// (apps/control-plane/internal/platform/http/router.go), so the page admits
// exactly accounts.is_platform_admin holders: the platform.admin permission.
export function isPlatformAdminViewer(viewer: RoleGateViewer): boolean {
  return can(viewer, "platform.admin");
}

// Feature gates and Marketplace are mounted behind WorkspaceAdminGate, which
// admits the OWNER of the selected workspace plus the platform-admin overlay.
export function isWorkspaceAdminViewer(viewer: RoleGateViewer): boolean {
  return (
    isPlatformAdminViewer(viewer) ||
    viewer.current_account.role === "owner"
  );
}
