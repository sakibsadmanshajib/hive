// Package auth — tenant_id fallback resolution for the OWUI shim path.
package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantFallback derives tenant_id/role for a Supabase-authenticated user
// whose JWT lacks the tenant_id custom claim. This happens specifically
// for access tokens minted through Supabase's OAuth-server (authorization
// code) grant -- used by Open WebUI's "Continue with Hive" SSO -- which
// does not invoke this project's custom_access_token_hook (see
// supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql).
// Direct sign-in (password/magic-link/refresh) tokens always carry the
// claim and never reach this path (#269).
//
// Resolve mirrors the hook's own derivation: the first ACTIVE membership
// in an unarchived tenant, ordered deterministically by join time. It is
// only ever consulted for requests that already passed through
// OWUIUnwrap's shim-key check (see IsOWUIUnwrapped) -- ordinary JWT auth
// (web-console, SDKs) is unaffected.
type TenantFallback struct {
	pool *pgxpool.Pool
}

// NewTenantFallback returns a resolver backed by pool. A nil pool is
// valid: Resolve then always reports "not found", equivalent to the
// fallback being disabled (e.g. deployments with no DB configured).
func NewTenantFallback(pool *pgxpool.Pool) *TenantFallback {
	return &TenantFallback{pool: pool}
}

// Resolve looks up userID's first active tenant membership. ok is false
// with a nil error when the pool is unset or no active membership
// exists -- both are "fallback unavailable", not error conditions the
// caller needs to distinguish from a clean miss.
func (f *TenantFallback) Resolve(ctx context.Context, userID uuid.UUID) (tenantID uuid.UUID, role string, ok bool, err error) {
	if f == nil || f.pool == nil {
		return uuid.Nil, "", false, nil
	}
	const q = `
		SELECT tu.tenant_id, tu.role
		FROM public.tenant_users tu
		JOIN public.tenants t ON t.id = tu.tenant_id
		WHERE tu.user_id = $1 AND tu.status = 'ACTIVE' AND t.archived_at IS NULL
		ORDER BY tu.joined_at ASC, tu.tenant_id ASC
		LIMIT 1`
	row := f.pool.QueryRow(ctx, q, userID)
	if scanErr := row.Scan(&tenantID, &role); scanErr != nil {
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return uuid.Nil, "", false, nil
		}
		return uuid.Nil, "", false, scanErr
	}
	return tenantID, strings.ToLower(role), true, nil
}
