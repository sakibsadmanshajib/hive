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
// membership, and admin-overlay flag. It is a pure mapping function, no DB
// calls, so it is safe to call from any handler that has already loaded the
// viewer context.
//
// A membership carries its role only while it is active. Every workspace-scoped
// permission in authz.Policy is granted on Role being owner or member, so
// blanking the role here denies the entire workspace surface for a seat that was
// merely offered.
//
// This is defence in depth and nothing more. It is not what protects the
// thirteen call sites today: every one of them either passes StatusActive as a
// literal or has already filtered to an active row, so this branch is currently
// unreachable in production. The real protection is EnsureViewerContext, which
// chooses only active memberships, and GetMembershipRole, which reads only
// active rows. This exists so that a future handler which starts passing a row
// straight from the database gets a denial instead of an escalation.
//
// The platform-admin overlay is deliberately untouched. It comes from its own
// query, which applies its own active-membership predicate, and it is not a
// property of this workspace membership.
func ActorFor(viewer auth.Viewer, chosen Membership, isAdmin bool) authz.Actor {
	role := chosen.Role
	if chosen.Status != StatusActive {
		role = ""
	}
	return authz.Actor{
		UserID:      viewer.UserID,
		WorkspaceID: chosen.AccountID,
		Role:        platform.MembershipRole(role),
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
			Status:    StatusActive,
		}

		isAdmin, err := roleSvc.IsPlatformAdmin(r.Context(), viewer.UserID)
		if err != nil {
			return authz.Actor{}, err
		}

		return ActorFor(viewer, chosen, isAdmin), nil
	}
}
