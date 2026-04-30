package platform

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxRoleStore is the concrete pgxpool-backed implementation of RoleStore.
// Phase 14 seeds this so main.go can wire RoleService for the owner-gated
// endpoints. Phase 18 may swap the backing query without changing the
// interface (RBAC contract stub locked).
type pgxRoleStore struct {
	pool *pgxpool.Pool
}

// NewPgxRoleStore returns a RoleStore backed by the given pgx pool. It
// queries `public.account_memberships` for owner-membership and
// `public.accounts.is_platform_admin` for the platform-admin flag.
func NewPgxRoleStore(pool *pgxpool.Pool) RoleStore {
	return &pgxRoleStore{pool: pool}
}

// GetMembershipRole returns the role for (userID, workspaceID).
//
// Returns:
//   - (role, nil)                       — when membership row exists
//   - ("", nil)                         — when workspace exists but no
//                                         membership row exists for userID
//   - ("", ErrWorkspaceNotFound)        — when workspaceID does not resolve
//                                         in public.accounts
func (s *pgxRoleStore) GetMembershipRole(ctx context.Context, userID, workspaceID uuid.UUID) (MembershipRole, error) {
	// First confirm the workspace (account) exists.
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.accounts WHERE id = $1)
	`, workspaceID).Scan(&exists)
	if err != nil {
		return "", fmt.Errorf("platform: account exists check: %w", err)
	}
	if !exists {
		return "", ErrWorkspaceNotFound
	}

	var role string
	err = s.pool.QueryRow(ctx, `
		SELECT role
		FROM public.account_memberships
		WHERE account_id = $1 AND user_id = $2
		LIMIT 1
	`, workspaceID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("platform: get membership role: %w", err)
	}
	return MembershipRole(role), nil
}

// IsPlatformAdmin returns whether userID owns at least one account row
// flagged with is_platform_admin = true. The flag lives on the workspace
// (account) so any owner of a flagged workspace is a platform admin —
// this matches the v1.1 single-tenant-admin model where the platform team
// owns its own internal workspace flagged as platform-admin.
func (s *pgxRoleStore) IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	var isAdmin bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM public.account_memberships m
			JOIN public.accounts a ON a.id = m.account_id
			WHERE m.user_id = $1
			  AND m.role = 'owner'
			  AND a.is_platform_admin = true
		)
	`, userID).Scan(&isAdmin)
	if err != nil {
		return false, fmt.Errorf("platform: is platform admin: %w", err)
	}
	return isAdmin, nil
}
