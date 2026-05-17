package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/platform"
)

// ErrNoViewer is returned by ActorResolver when no authenticated viewer is
// present in the request context. Middleware treats this as a 401.
var ErrNoViewer = errors.New("authz: no viewer in context")

// Actor represents the authenticated caller resolved before any authz check.
// UserID and WorkspaceID may be zero when not applicable (e.g. platform-scoped
// checks). Role is the workspace membership role. Verified is email_verified.
// IsAdmin is the platform_admin overlay from accounts.is_platform_admin.
type Actor struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID               // zero value for platform-scoped checks
	Role        platform.MembershipRole // "owner" | "member"
	Verified    bool                    // email_verified
	IsAdmin     bool                    // platform_admin overlay
}

// Policy is the stateless, zero-allocation authorization decision engine.
// Construct once at startup and share across all handlers.
type Policy struct{}

// NewPolicy returns a ready-to-use Policy.
func NewPolicy() Policy { return Policy{} }

// Can reports whether actor holds permission perm.
//
// Decision order (first matching rule wins):
//  1. perm not in registry -> deny (default-deny: typos / newly-declared
//     permissions without an explicit handler are rejected).
//  2. perm == PermPlatformAdmin or PermGrantsCreate -> only IsAdmin actors.
//  3. IsAdmin overlay -> grants all remaining permissions regardless of role/verified.
//  4. RequiresVerified(perm) && !actor.Verified -> deny.
//  5. billing.view, api_keys.read -> owner only (RequiresVerified=false, owner-scoped).
//  6. analytics.view, ledger.view -> any verified actor (owner OR member).
//  7. billing.write, api_keys.write, members.invite, members.manage,
//     workspace.settings -> owner only (verification gate already passed).
//  8. Default -> deny (any registry entry not explicitly handled above).
func (p Policy) Can(actor Actor, perm Permission) bool {
	// Default-deny for unknown permissions: anything not in the registry
	// (typos, perms declared without an explicit Can() case) is denied.
	if _, ok := registry[perm]; !ok {
		return false
	}

	// Platform-only perms: granted only to the admin overlay.
	if perm == PermPlatformAdmin || perm == PermGrantsCreate {
		return actor.IsAdmin
	}

	// Admin overlay grants every non-platform perm without further checks.
	if actor.IsAdmin {
		return true
	}

	// Verification gate: deny if perm requires verification and actor is unverified.
	if RequiresVerified(perm) && !actor.Verified {
		return false
	}

	switch perm {
	case PermBillingView, PermAPIKeysRead:
		// Read-only billing/key perms: RequiresVerified=false, owner-scoped.
		return actor.Role == platform.RoleOwner

	case PermAnalyticsView, PermLedgerView:
		// Audit: accounting/http.go:347, ledger/http.go:165, usage/http.go:393
		// all gate on !EmailVerified only — no role check. Any verified actor may view.
		return actor.Role == platform.RoleOwner || actor.Role == platform.RoleMember

	case PermBillingWrite, PermAPIKeysWrite, PermMembersInvite,
		PermMembersManage, PermWorkspaceSettings:
		// Owner-only write perms (verification gate already passed above).
		return actor.Role == platform.RoleOwner

	default:
		// Exhaustive switch: any registry entry not enumerated above is
		// denied. Adding a new Permission to the registry without a matching
		// case here will surface as a deny rather than a silent owner-grant.
		return false
	}
}

// AllGranted returns a sorted slice of permission wire strings for which
// Can(actor, perm) returns true. Used by accounts/service.go to produce the
// viewer-context permissions array.
func (p Policy) AllGranted(actor Actor) []string {
	perms := AllPermissions()
	granted := make([]string, 0, len(perms))
	for _, perm := range perms {
		if p.Can(actor, perm) {
			granted = append(granted, string(perm))
		}
	}
	sort.Strings(granted)
	return granted
}

// ActorResolver resolves an authenticated Actor from an HTTP request.
// Return ErrNoViewer when no authenticated viewer is present in the context.
type ActorResolver func(r *http.Request) (Actor, error)

// Middleware holds an ActorResolver for use with RequirePermission.
type Middleware struct {
	resolver ActorResolver
}

// NewMiddleware constructs a Middleware backed by the given ActorResolver.
func NewMiddleware(resolver ActorResolver) Middleware {
	return Middleware{resolver: resolver}
}

// RequirePermission returns an http.Handler middleware that gates access to
// next based on whether the resolved Actor holds perm.
//
// Response shape:
//   - 401 + {"error":"authentication required"} when resolver returns ErrNoViewer.
//   - 403 + {"error":"permission denied"} when Can returns false.
//   - delegates to next.ServeHTTP when Can returns true.
//
// Mirrors the provider-blind error shape of platform.RequirePlatformAdmin.
func (m Middleware) RequirePermission(perm Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, err := m.resolver(r)
			if err != nil {
				if errors.Is(err, ErrNoViewer) {
					writeAuthzError(w, http.StatusUnauthorized, "authentication required")
					return
				}
				writeAuthzError(w, http.StatusInternalServerError,
					fmt.Sprintf("authz: actor resolution failed: %s", err.Error()))
				return
			}
			pol := Policy{}
			if !pol.Can(actor, perm) {
				writeAuthzError(w, http.StatusForbidden, "permission denied")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authzError mirrors the provider-blind sanitised shape used in control-plane.
type authzError struct {
	Error string `json:"error"`
}

func writeAuthzError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(authzError{Error: msg})
}
