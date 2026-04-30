// Package platform — Phase 14 RBAC contract stub.
//
// This file owns the centralised owner-gate + platform-admin primitives used
// by every Phase 14 owner-/admin-gated handler:
//
//   - IsWorkspaceOwner(ctx, userID, workspaceID) (bool, error)
//   - IsPlatformAdmin(ctx, userID) (bool, error)
//   - RequirePlatformAdmin(http.Handler) http.Handler
//
// Phase 18 RBAC contract:
//
//	Phase 18 will replace the BODIES of the predicates below with a tier-aware
//	permission matrix evaluator. The SIGNATURES are the contract; do NOT
//	change them without updating the Phase 18 plan. Every Phase 14 owner-gated
//	handler imports this package and calls these functions exactly once at the
//	top of the handler — that is what gives Phase 18 a single seam to swap.
package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// ErrWorkspaceNotFound is the sentinel error returned by IsWorkspaceOwner when
// the workspaceID does not resolve to a known account. Callers MUST use
// errors.Is for comparison.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// MembershipRole captures the role string stored in account_memberships.role.
// Phase 14 only reads "owner"; Phase 18 will introduce the tier-aware matrix.
type MembershipRole string

const (
	RoleOwner  MembershipRole = "owner"
	RoleMember MembershipRole = "member"
)

// RoleStore is the narrow data-access interface required by the platform role
// primitives. The production implementation lives in
// apps/control-plane/internal/platform/db (or accounts) and queries
// public.account_memberships + public.accounts.is_platform_admin.
//
// Defined here (where it is used) per Go interface-placement convention.
type RoleStore interface {
	// GetMembershipRole returns the role for (userID, workspaceID).
	// Returns ("", ErrWorkspaceNotFound) when no workspace exists.
	// Returns ("", nil) when workspace exists but the user has no membership.
	GetMembershipRole(ctx context.Context, userID, workspaceID uuid.UUID) (MembershipRole, error)

	// IsPlatformAdmin returns whether userID owns at least one account with
	// is_platform_admin = true. Returns (false, nil) for normal users.
	IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

// RoleService wraps a RoleStore behind the public predicates. Construct via
// NewRoleService and inject into HTTP wiring.
type RoleService struct {
	store RoleStore
}

// NewRoleService returns a RoleService backed by the given store.
func NewRoleService(store RoleStore) *RoleService {
	return &RoleService{store: store}
}

// IsWorkspaceOwner reports whether userID has role='owner' on workspaceID.
//
// Returns:
//   - (true,  nil)                       — owner row exists
//   - (false, nil)                       — non-owner membership or no membership
//   - (false, ErrWorkspaceNotFound)      — workspaceID does not exist
//   - (false, err)                       — propagated store error
//
// Phase 18: replace this body with the tier-aware permission matrix evaluator.
func (s *RoleService) IsWorkspaceOwner(ctx context.Context, userID, workspaceID uuid.UUID) (bool, error) {
	role, err := s.store.GetMembershipRole(ctx, userID, workspaceID)
	if err != nil {
		return false, err
	}
	return role == RoleOwner, nil
}

// IsPlatformAdmin reports whether userID is flagged as a platform admin via
// the accounts.is_platform_admin column.
//
// Phase 18: replace this body with the tier-aware permission matrix evaluator.
func (s *RoleService) IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.store.IsPlatformAdmin(ctx, userID)
}

// RequirePlatformAdmin returns middleware that gates an http.Handler to
// platform admins only. Non-admins receive a provider-blind sanitized 403.
// Unauthenticated requests receive 401 (auth.Middleware should run first).
//
// Phase 18 RBAC contract: the SIGNATURE of this middleware is locked. Phase 18
// may swap the body to consult the tier-aware matrix.
func (s *RoleService) RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewer, ok := auth.ViewerFromContext(r.Context())
		if !ok {
			writeForbidden(w, http.StatusUnauthorized, "authentication required")
			return
		}
		isAdmin, err := s.IsPlatformAdmin(r.Context(), viewer.UserID)
		if err != nil {
			writeForbidden(w, http.StatusInternalServerError, "platform admin check failed")
			return
		}
		if !isAdmin {
			writeForbidden(w, http.StatusForbidden, "platform admin permission required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// errorResponse mirrors the provider-blind sanitized shape used elsewhere in
// control-plane (see internal/auth/middleware.go writeUnauthorized).
type errorResponse struct {
	Error string `json:"error"`
}

func writeForbidden(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
