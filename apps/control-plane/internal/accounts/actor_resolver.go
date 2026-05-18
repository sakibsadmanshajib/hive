package accounts

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// AdminChecker is the narrow interface used by NewActorResolver to look up the
// platform-admin overlay. *platform.RoleService satisfies this interface.
type AdminChecker interface {
	IsPlatformAdmin(ctx interface {
		Deadline() (interface{}, bool)
		Done() <-chan struct{}
		Err() error
		Value(interface{}) interface{}
	}, userID uuid.UUID) (bool, error)
}

// ActorFor builds an authz.Actor from the already-resolved viewer, chosen
// membership, and admin-overlay flag. It is a pure mapping function — no DB
// calls — so it is safe to call from any handler that has already loaded the
// viewer context.
func ActorFor(viewer auth.Viewer, chosen Membership, isAdmin bool) authz.Actor {
	return authz.Actor{
		UserID:      viewer.UserID,
		WorkspaceID: chosen.AccountID,
		Role:        platform.MembershipRole(chosen.Role),
		Verified:    viewer.EmailVerified,
		IsAdmin:     isAdmin,
	}
}

// NewActorResolver returns an authz.ActorResolver closure backed by the
// accounts Service and a platform.RoleService. The resolver:
//  1. Reads the authenticated viewer from the request context (returns
//     authz.ErrNoViewer when absent).
//  2. Resolves the current workspace via EnsureViewerContext (honouring the
//     X-Hive-Account-ID header).
//  3. Looks up the platform-admin overlay via roleSvc.IsPlatformAdmin.
//  4. Returns authz.Actor via ActorFor.
//
// The resolver is constructed once at startup and shared across all middleware
// instances; it is safe for concurrent use.
func NewActorResolver(svc *Service, roleSvc *platform.RoleService) authz.ActorResolver {
	return func(r *http.Request) (authz.Actor, error) {
		viewer, ok := auth.ViewerFromContext(r.Context())
		if !ok {
			return authz.Actor{}, authz.ErrNoViewer
		}

		requestedAccountID := parseAccountHeader(r)
		vc, err := svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
		if err != nil {
			return authz.Actor{}, err
		}

		// Find the chosen membership to get the workspace-scoped role.
		// EnsureViewerContext already resolved the chosen account; we need the
		// matching membership row for the role string.
		chosen := Membership{
			AccountID: vc.CurrentAccount.ID,
			UserID:    viewer.UserID,
			Role:      vc.CurrentAccount.Role,
			Status:    "active",
		}

		isAdmin, err := roleSvc.IsPlatformAdmin(r.Context(), viewer.UserID)
		if err != nil {
			return authz.Actor{}, err
		}

		return ActorFor(viewer, chosen, isAdmin), nil
	}
}
