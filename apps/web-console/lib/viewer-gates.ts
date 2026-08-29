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
//
// This is a best-effort UX-layer mirror, not a guaranteed match: the
// control-plane's WorkspaceAdminGate resolves ownership from
// public.tenant_users.role, while `current_account.role` here traces to the
// separate public.account_memberships.role. Until issue #1245's fix
// (accounts.Service.UpdateMemberRole propagating onto tenant_users via
// signup.SyncTenantMembershipRole), the two tables could diverge permanently
// after any Members-page role change: only account_memberships was ever
// updated post-signup, so a legitimate, currently-promoted co-owner passed
// this check but then got 403'd by the real backend gate on the data fetch,
// landing on the "managed by your administrator" empty state instead of
// their real dashboard (issue #1244). UpdateMemberRole now keeps
// tenant_users.role in sync with every account_memberships role change it
// makes, so the two stay aligned going forward; this frontend check needed
// no change; account_memberships was already the correct signal to read
// here; it was the backend gate that was reading a stale copy. A genuine
// non-owner is denied correctly here regardless of any of this, so the
// customer-facing threat this file exists for (#947/#948/#949) was never
// affected either way.
export function isWorkspaceAdminViewer(viewer: RoleGateViewer): boolean {
  return (
    isPlatformAdminViewer(viewer) ||
    viewer.current_account.role === "owner"
  );
}
