package egress

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the narrow data-access port for the egress policy store.
// GetTenantDefault and GetUserOverride return ErrNotFound when the row is
// absent; the merge into an effective policy is Service's job, not the
// repository's.
type Repository interface {
	GetTenantDefault(ctx context.Context, tenantID uuid.UUID) (Policy, error)
	GetUserOverride(ctx context.Context, tenantID, userID uuid.UUID) (Policy, error)
	UpsertTenantDefault(ctx context.Context, tenantID uuid.UUID, allowedHosts []string) (Policy, error)
	UpsertUserOverride(ctx context.Context, tenantID, userID uuid.UUID, allowedHosts []string) (Policy, error)
	DeleteUserOverride(ctx context.Context, tenantID, userID uuid.UUID) error
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository constructs a pgxpool-backed Repository.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

// withTenantTx runs fn inside an explicit transaction with the RLS session
// variable set LOCAL (transaction-scoped) to tenantID. hive_app is NOT
// BYPASSRLS (20260518_04_phase19_audit_rls_and_indexes.sql), so every query
// against public.egress_policies must see app.current_tenant_id set to the
// caller's tenant.
//
// LOCAL (is_local=true), not session scope, and inside an explicit
// transaction — two prior attempts got this wrong:
//   - A bare conn.Exec(set_config(..., true)) followed by a separate
//     conn.QueryRow with no transaction: LOCAL resets the instant the Exec's
//     own implicit (autocommit) transaction ends, so the following query saw
//     current_setting() back at NULL. Verified against live Postgres 16:
//     RLS's own tenant_id = current_setting(...)::uuid cast then errors on
//     an empty string.
//   - Switching is_local to false (session scope) "fixed" that, but this
//     repo's pgxpool has no AfterRelease reset hook, so the setting survives
//     Release and leaks to whatever tenant's request borrows that physical
//     connection next — a cross-tenant data leak, worse than the original
//     bug on a data-sovereignty product.
//
// Wrapping in Begin/Commit makes LOCAL correct: it applies for exactly this
// transaction's statements and is guaranteed to clear at Commit or Rollback,
// so nothing survives onto the pooled connection for the next borrower.
func (r *pgxRepository) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("egress: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("egress: set tenant: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgxRepository) GetTenantDefault(ctx context.Context, tenantID uuid.UUID) (Policy, error) {
	var p Policy
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id, allowed_hosts, updated_at
			  FROM public.egress_policies
			 WHERE tenant_id = $1 AND user_id IS NULL
		`, tenantID)
		got, err := scanTenantDefault(row)
		p = got
		return err
	})
	if err != nil {
		return Policy{}, err
	}
	return p, nil
}

func (r *pgxRepository) GetUserOverride(ctx context.Context, tenantID, userID uuid.UUID) (Policy, error) {
	var p Policy
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id, user_id, allowed_hosts, updated_at
			  FROM public.egress_policies
			 WHERE tenant_id = $1 AND user_id = $2
		`, tenantID, userID)
		got, err := scanUserOverride(row)
		p = got
		return err
	})
	if err != nil {
		return Policy{}, err
	}
	return p, nil
}

func (r *pgxRepository) UpsertTenantDefault(ctx context.Context, tenantID uuid.UUID, allowedHosts []string) (Policy, error) {
	var p Policy
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO public.egress_policies (tenant_id, user_id, allowed_hosts)
			VALUES ($1, NULL, $2)
			ON CONFLICT (tenant_id) WHERE user_id IS NULL
			DO UPDATE SET allowed_hosts = EXCLUDED.allowed_hosts, updated_at = now()
			RETURNING tenant_id, allowed_hosts, updated_at
		`, tenantID, allowedHosts)
		got, err := scanTenantDefault(row)
		p = got
		return err
	})
	if err != nil {
		return Policy{}, err
	}
	return p, nil
}

func (r *pgxRepository) UpsertUserOverride(ctx context.Context, tenantID, userID uuid.UUID, allowedHosts []string) (Policy, error) {
	var p Policy
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO public.egress_policies (tenant_id, user_id, allowed_hosts)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, user_id) WHERE user_id IS NOT NULL
			DO UPDATE SET allowed_hosts = EXCLUDED.allowed_hosts, updated_at = now()
			RETURNING tenant_id, user_id, allowed_hosts, updated_at
		`, tenantID, userID, allowedHosts)
		got, err := scanUserOverride(row)
		p = got
		return err
	})
	if err != nil {
		return Policy{}, err
	}
	return p, nil
}

func (r *pgxRepository) DeleteUserOverride(ctx context.Context, tenantID, userID uuid.UUID) error {
	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			DELETE FROM public.egress_policies WHERE tenant_id = $1 AND user_id = $2
		`, tenantID, userID)
		if err != nil {
			return fmt.Errorf("egress: delete user override: %w", err)
		}
		return nil
	})
}

type scanner interface {
	Scan(dest ...any) error
}

// scanTenantDefault scans a row with no user_id column selected — the tenant
// default row always has UserID == uuid.Nil by construction.
func scanTenantDefault(s scanner) (Policy, error) {
	var p Policy
	if err := s.Scan(&p.TenantID, &p.AllowedHosts, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Policy{}, ErrNotFound
		}
		return Policy{}, fmt.Errorf("egress: scan tenant default: %w", err)
	}
	return p, nil
}

func scanUserOverride(s scanner) (Policy, error) {
	var p Policy
	if err := s.Scan(&p.TenantID, &p.UserID, &p.AllowedHosts, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Policy{}, ErrNotFound
		}
		return Policy{}, fmt.Errorf("egress: scan user override: %w", err)
	}
	return p, nil
}
