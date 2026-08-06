package platform

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// platformAdminCtxKey is the context key carrying whether the request's caller
// holds the platform-admin overlay. Stamped by WorkspaceAdminGate.Require so a
// handler behind that gate can tell an ordinary workspace owner apart from a
// platform operator without repeating the lookup.
type platformAdminCtxKey struct{}

// WithPlatformAdmin returns ctx carrying the caller's platform-admin overlay.
func WithPlatformAdmin(ctx context.Context, isAdmin bool) context.Context {
	return context.WithValue(ctx, platformAdminCtxKey{}, isAdmin)
}

// PlatformAdminFromContext reports whether the request context was stamped with
// the platform-admin overlay. It returns false when the stamp is absent, so a
// handler mounted without WorkspaceAdminGate fails closed on the platform-only
// operations it guards rather than silently granting them.
func PlatformAdminFromContext(ctx context.Context) bool {
	isAdmin, _ := ctx.Value(platformAdminCtxKey{}).(bool)
	return isAdmin
}

// WorkspaceAdminGate admits the administrator of the workspace in scope: either
// the OWNER of the tenant the caller has selected, or a platform admin.
//
// It exists because platform-admin was the wrong gate for workspace-scoped
// administration. Feature gates and marketplace enablement are per-tenant rows,
// so the person who owns the organization is their administrator, while credit
// minting and provider base-URL rewrites stay platform-only. An account that
// owns its workspace but carries no platform-admin flag could reach neither
// surface before this gate existed (issue #758).
//
// Scope comes from auth.Viewer.TenantID, which auth.Client has already validated
// against a live membership, and the owner predicate then requires an ACTIVE
// OWNER row on that same tenant. An OWNER of tenant A therefore cannot reach
// tenant B: selecting B yields no OWNER row in B and the gate denies. That is
// deliberately unlike the account-scoped IsPlatformAdmin overlay, which grants
// on ANY owner membership of ANY flagged account and is the cross-workspace leak
// tracked by issue #750. Nothing here consults that overlay for the
// workspace-scoped decision; it is read only to widen access for a platform
// operator, whose reach across tenants is the intended behaviour.
type WorkspaceAdminGate struct {
	tenants *TenantRoleService
	roles   *RoleService
}

// NewWorkspaceAdminGate builds the gate. Either dependency may be nil, in which
// case that half of the check is treated as not granted rather than skipped: a
// nil RoleService means nobody receives the platform-admin overlay, and a nil
// TenantRoleService means nobody passes as a workspace owner.
func NewWorkspaceAdminGate(tenants *TenantRoleService, roles *RoleService) *WorkspaceAdminGate {
	return &WorkspaceAdminGate{tenants: tenants, roles: roles}
}

// Require gates next on workspace-administrator authority and stamps the
// platform-admin overlay into the request context, for handlers that carve out
// platform-only operations behind the same mount.
//
// Responses mirror RequirePlatformAdmin's provider-blind shape: 401 when
// unauthenticated, 400 when no tenant is selected, 403 when the caller is
// neither workspace owner nor platform admin, 500 on a lookup failure.
func (g *WorkspaceAdminGate) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewer, ok := auth.ViewerFromContext(r.Context())
		if !ok {
			writeForbidden(w, http.StatusUnauthorized, "authentication required")
			return
		}

		isAdmin := false
		if g.roles != nil {
			var err error
			isAdmin, err = g.roles.IsPlatformAdmin(r.Context(), viewer.UserID)
			if err != nil {
				writeForbidden(w, http.StatusInternalServerError, "platform admin check failed")
				return
			}
		}
		ctx := WithPlatformAdmin(r.Context(), isAdmin)
		if isAdmin {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if viewer.TenantID == uuid.Nil {
			writeForbidden(w, http.StatusBadRequest, "no tenant selected")
			return
		}
		if g.tenants == nil {
			writeForbidden(w, http.StatusForbidden, "workspace owner permission required")
			return
		}
		isOwner, err := g.tenants.IsTenantOwner(r.Context(), viewer.UserID, viewer.TenantID)
		if err != nil {
			writeForbidden(w, http.StatusInternalServerError, "workspace owner check failed")
			return
		}
		if !isOwner {
			writeForbidden(w, http.StatusForbidden, "workspace owner permission required")
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
