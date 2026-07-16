package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the narrow data-access port for the marketplace catalog
// (global, admin-curated — no tenant scoping) and its per-tenant enablement
// (RLS-scoped, mirrors apps/control-plane/internal/egress).
type Repository interface {
	ListEntries(ctx context.Context) ([]Entry, error)
	GetEntry(ctx context.Context, id uuid.UUID) (Entry, error)
	CreateEntry(ctx context.Context, e Entry) (Entry, error)
	UpdateEntry(ctx context.Context, id uuid.UUID, name, description string, config json.RawMessage) (Entry, error)
	DeleteEntry(ctx context.Context, id uuid.UUID) error

	EnabledEntryIDs(ctx context.Context, tenantID uuid.UUID) (map[uuid.UUID]TenantEntry, error)
	SetEnabled(ctx context.Context, tenantID, entryID uuid.UUID, enabled bool, actorID uuid.UUID) error
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository constructs a pgxpool-backed Repository.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

// ListEntries returns every catalog entry, ordered by (kind, name) so the
// admin UI groups stably — same ordering contract as
// apps/control-plane/internal/tenant/settings.Resolver.Registry.
func (r *pgxRepository) ListEntries(ctx context.Context) ([]Entry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, kind, name, description, config, created_by, created_at, updated_at
		  FROM public.marketplace_entries
		 ORDER BY kind, name`)
	if err != nil {
		return nil, fmt.Errorf("marketplace: list entries: %w", err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace: iterate entries: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) GetEntry(ctx context.Context, id uuid.UUID) (Entry, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, kind, name, description, config, created_by, created_at, updated_at
		  FROM public.marketplace_entries
		 WHERE id = $1`, id)
	return scanEntry(row)
}

func (r *pgxRepository) CreateEntry(ctx context.Context, e Entry) (Entry, error) {
	var createdBy any
	if e.CreatedBy != uuid.Nil {
		createdBy = e.CreatedBy
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.marketplace_entries (kind, name, description, config, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, kind, name, description, config, created_by, created_at, updated_at
	`, string(e.Kind), e.Name, e.Description, []byte(e.Config), createdBy)
	entry, err := scanEntry(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Entry{}, ErrDuplicate
		}
		return Entry{}, err
	}
	return entry, nil
}

func (r *pgxRepository) UpdateEntry(ctx context.Context, id uuid.UUID, name, description string, config json.RawMessage) (Entry, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE public.marketplace_entries
		   SET name = $2, description = $3, config = $4, updated_at = now()
		 WHERE id = $1
		RETURNING id, kind, name, description, config, created_by, created_at, updated_at
	`, id, name, description, []byte(config))
	return scanEntry(row)
}

func (r *pgxRepository) DeleteEntry(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM public.marketplace_entries WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("marketplace: delete entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// withTenantTx mirrors apps/control-plane/internal/egress/repository.go's
// helper of the same name: hive_app is NOT BYPASSRLS
// (20260518_04_phase19_audit_rls_and_indexes.sql), so every query against
// public.marketplace_tenant_entries must run inside an explicit transaction
// with app.current_tenant_id set LOCAL — guaranteed to clear at Commit or
// Rollback so nothing survives onto the pooled connection for the next
// borrower. See egress/repository.go for the full rationale (two prior
// attempts at this got it wrong: a bare Exec loses LOCAL scope at the next
// statement, and session-scope leaks cross-tenant on connection reuse).
func (r *pgxRepository) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("marketplace: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("marketplace: set tenant: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgxRepository) EnabledEntryIDs(ctx context.Context, tenantID uuid.UUID) (map[uuid.UUID]TenantEntry, error) {
	out := make(map[uuid.UUID]TenantEntry)
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT entry_id, enabled_by, enabled_at
			  FROM public.marketplace_tenant_entries
			 WHERE tenant_id = $1
		`, tenantID)
		if err != nil {
			return fmt.Errorf("marketplace: query enabled entries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var te TenantEntry
			var enabledBy *uuid.UUID
			if err := rows.Scan(&te.EntryID, &enabledBy, &te.EnabledAt); err != nil {
				return fmt.Errorf("marketplace: scan enabled entry: %w", err)
			}
			if enabledBy != nil {
				te.EnabledBy = *enabledBy
			}
			out[te.EntryID] = te
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *pgxRepository) SetEnabled(ctx context.Context, tenantID, entryID uuid.UUID, enabled bool, actorID uuid.UUID) error {
	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if !enabled {
			if _, err := tx.Exec(ctx, `
				DELETE FROM public.marketplace_tenant_entries
				 WHERE tenant_id = $1 AND entry_id = $2
			`, tenantID, entryID); err != nil {
				return fmt.Errorf("marketplace: disable entry: %w", err)
			}
			return nil
		}

		var actor any
		if actorID != uuid.Nil {
			actor = actorID
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO public.marketplace_tenant_entries (tenant_id, entry_id, enabled_by)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, entry_id)
			DO UPDATE SET enabled_by = EXCLUDED.enabled_by, enabled_at = now()
		`, tenantID, entryID, actor)
		if err != nil {
			if isForeignKeyViolation(err) {
				return ErrNotFound
			}
			return fmt.Errorf("marketplace: enable entry: %w", err)
		}
		return nil
	})
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(s scanner) (Entry, error) {
	var e Entry
	var kind string
	var createdBy *uuid.UUID
	var config []byte
	if err := s.Scan(&e.ID, &kind, &e.Name, &e.Description, &config, &createdBy, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, fmt.Errorf("marketplace: scan entry: %w", err)
	}
	e.Kind = Kind(kind)
	e.Config = json.RawMessage(config)
	if createdBy != nil {
		e.CreatedBy = *createdBy
	}
	return e, nil
}

// isUniqueViolation mirrors apps/control-plane/internal/providers/repository.go's
// helper of the same name (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation reports whether err is a Postgres foreign-key
// violation (SQLSTATE 23503) — hit when SetEnabled(true, ...) names an
// entryID that no longer exists in public.marketplace_entries.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
