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

func (r *pgxRepository) GetTenantDefault(ctx context.Context, tenantID uuid.UUID) (Policy, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT tenant_id, allowed_hosts, updated_at
		  FROM public.egress_policies
		 WHERE tenant_id = $1 AND user_id IS NULL
	`, tenantID)
	return scanTenantDefault(row)
}

func (r *pgxRepository) GetUserOverride(ctx context.Context, tenantID, userID uuid.UUID) (Policy, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT tenant_id, user_id, allowed_hosts, updated_at
		  FROM public.egress_policies
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID)
	return scanUserOverride(row)
}

func (r *pgxRepository) UpsertTenantDefault(ctx context.Context, tenantID uuid.UUID, allowedHosts []string) (Policy, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.egress_policies (tenant_id, user_id, allowed_hosts)
		VALUES ($1, NULL, $2)
		ON CONFLICT (tenant_id) WHERE user_id IS NULL
		DO UPDATE SET allowed_hosts = EXCLUDED.allowed_hosts, updated_at = now()
		RETURNING tenant_id, allowed_hosts, updated_at
	`, tenantID, allowedHosts)
	return scanTenantDefault(row)
}

func (r *pgxRepository) UpsertUserOverride(ctx context.Context, tenantID, userID uuid.UUID, allowedHosts []string) (Policy, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.egress_policies (tenant_id, user_id, allowed_hosts)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) WHERE user_id IS NOT NULL
		DO UPDATE SET allowed_hosts = EXCLUDED.allowed_hosts, updated_at = now()
		RETURNING tenant_id, user_id, allowed_hosts, updated_at
	`, tenantID, userID, allowedHosts)
	return scanUserOverride(row)
}

func (r *pgxRepository) DeleteUserOverride(ctx context.Context, tenantID, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM public.egress_policies WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("egress: delete user override: %w", err)
	}
	return nil
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
