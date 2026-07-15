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

// withTenantConn acquires a dedicated connection, sets the RLS session
// variable the egress_policies tenant-isolation policy checks, and runs fn on
// that SAME connection before releasing it. hive_app is NOT BYPASSRLS (see
// 20260518_04_phase19_audit_rls_and_indexes.sql), so every query against
// public.egress_policies MUST run on a connection with app.current_tenant_id
// set — using r.pool.QueryRow/Exec directly evaluates the RLS policy against
// NULL and silently returns zero rows / fails every write.
//
// is_local is FALSE (session-scoped), not true. A bare conn.Exec followed by
// a separate conn.QueryRow — with no explicit BEGIN — are two independent
// autocommit transactions on the wire; set_config(..., true) (transaction-
// local) resets the moment the Exec's implicit transaction completes, so the
// following query would see current_setting() back at NULL. Verified
// empirically against a real Postgres 16 instance (psql -c set_config(...,
// true) followed by a separate -c current_setting() returns empty; the same
// with false returns the value). Session scope is still safe here because
// this is the ONLY code path that touches public.egress_policies, and every
// call re-sets app.current_tenant_id before it queries, so a pooled
// connection recycled for a different tenant can never read with a stale
// value. internal/rag/repository.go uses is_local=true across separate
// Exec/QueryRow calls with no explicit transaction — that looks like the
// same bug, but it is out of scope here; flagged separately.
func (r *pgxRepository) withTenantConn(ctx context.Context, tenantID uuid.UUID, fn func(conn *pgxpool.Conn) error) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("egress: acquire conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, false)", tenantID.String()); err != nil {
		return fmt.Errorf("egress: set tenant: %w", err)
	}
	return fn(conn)
}

func (r *pgxRepository) GetTenantDefault(ctx context.Context, tenantID uuid.UUID) (Policy, error) {
	var p Policy
	err := r.withTenantConn(ctx, tenantID, func(conn *pgxpool.Conn) error {
		row := conn.QueryRow(ctx, `
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
	err := r.withTenantConn(ctx, tenantID, func(conn *pgxpool.Conn) error {
		row := conn.QueryRow(ctx, `
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
	err := r.withTenantConn(ctx, tenantID, func(conn *pgxpool.Conn) error {
		row := conn.QueryRow(ctx, `
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
	err := r.withTenantConn(ctx, tenantID, func(conn *pgxpool.Conn) error {
		row := conn.QueryRow(ctx, `
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
	return r.withTenantConn(ctx, tenantID, func(conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `
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
