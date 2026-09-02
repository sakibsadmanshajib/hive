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
export interface RoleGateViewer extends ViewerWithPermissions {
  workspace_admin: boolean;
}

// Providers is mounted behind control-plane RequirePlatformAdmin
// (apps/control-plane/internal/platform/http/router.go), so the page admits
// exactly accounts.is_platform_admin holders: the platform.admin permission.
export function isPlatformAdminViewer(viewer: RoleGateViewer): boolean {
  return can(viewer, "platform.admin");
}

// Feature gates and Marketplace are mounted behind WorkspaceAdminGate, which
// admits the OWNER of the selected workspace plus the platform-admin overlay.
//
// `workspace_admin` is that gate's own answer, computed by the control-plane
// from public.tenant_users for the tenant this caller has selected and widened
// by the same platform-admin overlay. Reading it here is what keeps this
// predicate from being a second, independently drifting opinion.
//
// It used to read `current_account.role === "owner"`, which traces to the
// separate public.account_memberships. Two tables answering one question
// produced two live defects. Issue #1244: a promoted co-owner passed here and
// was then 403'd by the real gate, because only account_memberships was
// updated post-signup (fixed at the writer by #1245). Issue #1660: a personal
// tenant's sole owner is 'owner' in account_memberships and deliberately
// 'MEMBER' in tenant_users (signup.insertPersonalMembership), so this
// predicate admitted them to a page whose data fetch was refused, leaving
// them on an empty state that told them to ask an administrator who does not
// exist. Both are the same shape: the console decided from a table the
// backend does not gate on.
//
// Fail-closed by construction: a control-plane that omits the field decodes to
// false, so the surface hides rather than rendering for a caller the backend
// will refuse.
export function isWorkspaceAdminViewer(viewer: RoleGateViewer): boolean {
  return isPlatformAdminViewer(viewer) || viewer.workspace_admin;
}
