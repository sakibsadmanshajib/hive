package platform

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxRoleStore is the concrete pgxpool-backed implementation of both RoleStore
// (account-scoped, public.accounts + public.account_memberships) and
// TenantRoleStore (tenant-scoped, public.tenant_users). One struct because both
// need nothing but the pool; the two constructors below return the narrow
// interface each caller wants.
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

// NewPgxTenantRoleStore returns a TenantRoleStore backed by the given pgx pool.
// It queries `public.tenant_users`, the tenant-scoped role table, which is a
// different id space from the account tables NewPgxRoleStore reads.
func NewPgxTenantRoleStore(pool *pgxpool.Pool) TenantRoleStore {
	return &pgxRoleStore{pool: pool}
}

// GetMembershipRole returns the role for (userID, workspaceID), considering
// only ACTIVE memberships.
//
// public.account_memberships.status is either 'active' or 'invited', and an
// invited row records an offered seat, not an accepted one. Without the status
// predicate a user invited as owner held owner authority on the workspace
// (budget writes, egress policy) before accepting the invitation.
//
// Returns:
//   - (role, nil) when an active membership row exists.
//   - ("", nil) when the workspace exists but userID has no active membership
//     row on it.
//   - ("", ErrWorkspaceNotFound) when workspaceID does not resolve in
//     public.accounts.
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

	// Issue #896: account_memberships' hive_app policy requires
	// app.current_actor_user_id set LOCAL to the caller's own id (Shape A --
	// every call site passes the caller's own userID here, never a target
	// user's). See apps/control-plane/internal/accounts/repository.go's
	// withActorTx doc comment for why this needs an explicit transaction
	// rather than a bare set_config + separate query.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("platform: begin membership role tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_actor_user_id', $1, true)", userID.String()); err != nil {
		return "", fmt.Errorf("platform: set actor scope: %w", err)
	}

	var role string
	err = tx.QueryRow(ctx, `
		SELECT role
		FROM public.account_memberships
		WHERE account_id = $1 AND user_id = $2 AND status = 'active'
		LIMIT 1
	`, workspaceID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("platform: get membership role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("platform: commit membership role tx: %w", err)
	}
	return MembershipRole(role), nil
}

// GetTenantRole returns the ACTIVE tenant_users role for (userID, tenantID),
// or ("", nil) when no active membership exists.
//
// Runs inside an explicit transaction with app.current_tenant_id set LOCAL,
// because hive_app is NOT BYPASSRLS and the tenant_users policy this query
// relies on is keyed on that setting
// (20260726_01_tenant_users_hive_app_grant.sql). LOCAL scope inside an explicit
// transaction is required, not incidental: see the withTenantTx comment in
// apps/control-plane/internal/egress/repository.go for the two ways this was
// gotten wrong before, one of which leaked the setting across tenants on a
// pooled connection.
func (s *pgxRoleStore) GetTenantRole(ctx context.Context, userID, tenantID uuid.UUID) (TenantRole, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("platform: begin tenant role tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return "", fmt.Errorf("platform: set tenant scope: %w", err)
	}

	var role string
	err = tx.QueryRow(ctx, `
		SELECT role
		FROM public.tenant_users
		WHERE tenant_id = $1 AND user_id = $2 AND status = 'ACTIVE'
		LIMIT 1
	`, tenantID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("platform: get tenant role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("platform: commit tenant role tx: %w", err)
	}
	return TenantRole(role), nil
}

// IsPlatformAdmin returns whether userID holds an ACTIVE owner membership on
// at least one account row flagged with is_platform_admin = true. The flag
// lives on the workspace (account) so any active owner of a flagged workspace
// is a platform admin. This matches the v1.1 single-tenant-admin model where
// the platform team owns its own internal workspace flagged as platform-admin.
//
// The status predicate is load-bearing, not defensive tidiness. This predicate
// gates POST /v1/admin/credit-grants, which mints credit, and the provider
// administration surface. Without it, anyone invited as owner to a
// platform-admin account held both from the moment the membership row was
// written, without ever accepting the invitation.
func (s *pgxRoleStore) IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	// Issue #896: same actor-scope requirement as GetMembershipRole above.
	// Every call site (WorkspaceAdminGate.Require, accounts.Service twice)
	// passes viewer.UserID, the caller's own id, never a target user's.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("platform: begin platform admin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_actor_user_id', $1, true)", userID.String()); err != nil {
		return false, fmt.Errorf("platform: set actor scope: %w", err)
	}

	var isAdmin bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM public.account_memberships m
			JOIN public.accounts a ON a.id = m.account_id
			WHERE m.user_id = $1
			  AND m.role = 'owner'
			  AND m.status = 'active'
			  AND a.is_platform_admin = true
		)
	`, userID).Scan(&isAdmin); err != nil {
		return false, fmt.Errorf("platform: is platform admin: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("platform: commit platform admin tx: %w", err)
	}
	return isAdmin, nil
}
