package usermemories

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the narrow data-access port for public.user_memories. Every
// method is scoped by the (tenantID, userID) pair its caller resolved from
// the URL path, and every query filters on user_id explicitly: tenant scope
// comes from RLS (app.current_tenant_id), user scope from the WHERE clause,
// the same split public.agent_tasks uses.
type Repository interface {
	Create(ctx context.Context, tenantID, userID uuid.UUID, content string, sourceChatID *string) (Memory, error)
	Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Memory, error)
	List(ctx context.Context, tenantID, userID uuid.UUID) ([]Memory, error)
	Update(ctx context.Context, tenantID, userID, id uuid.UUID, content string) (Memory, error)
	Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error
	// EvictOldest deletes all but the newest keep rows for the user, oldest
	// first, so the service-layer cap holds without a hard failure on
	// overflow.
	EvictOldest(ctx context.Context, tenantID, userID uuid.UUID, keep int) (int64, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository constructs a pgxpool-backed Repository.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

// withTenantTx mirrors apps/control-plane/internal/agenttask/repository.go's
// helper of the same name: hive_app is NOT BYPASSRLS
// (20260518_04_phase19_audit_rls_and_indexes.sql), so every query against
// public.user_memories must run inside an explicit transaction with
// app.current_tenant_id set LOCAL, guaranteed to clear at Commit or Rollback
// so nothing survives onto the pooled connection for the next borrower.
func (r *pgxRepository) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("usermemories: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("usermemories: withTenantTx: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const memoryColumns = `id, tenant_id, user_id, content, source_chat_id, created_at, updated_at`

func (r *pgxRepository) Create(ctx context.Context, tenantID, userID uuid.UUID, content string, sourceChatID *string) (Memory, error) {
	var m Memory
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO public.user_memories (tenant_id, user_id, content, source_chat_id)
			VALUES ($1, $2, $3, $4)
			RETURNING `+memoryColumns+`
		`, tenantID, userID, content, sourceChatID)
		var err error
		m, err = scanMemory(row)
		return err
	})
	if err != nil {
		return Memory{}, fmt.Errorf("usermemories: create: %w", err)
	}
	return m, nil
}

func (r *pgxRepository) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Memory, error) {
	var m Memory
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT `+memoryColumns+`
			  FROM public.user_memories
			 WHERE id = $1 AND user_id = $2
		`, id, userID)
		var err error
		m, err = scanMemory(row)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Memory{}, ErrNotFound
		}
		return Memory{}, fmt.Errorf("usermemories: get: %w", err)
	}
	return m, nil
}

func (r *pgxRepository) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Memory, error) {
	var out []Memory
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+memoryColumns+`
			  FROM public.user_memories
			 WHERE user_id = $1
			 ORDER BY created_at DESC, id DESC
		`, userID)
		if err != nil {
			return fmt.Errorf("usermemories: list query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			m, scanErr := scanMemory(rows)
			if scanErr != nil {
				return scanErr
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("usermemories: list: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) Update(ctx context.Context, tenantID, userID, id uuid.UUID, content string) (Memory, error) {
	var m Memory
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE public.user_memories
			   SET content = $3,
			       updated_at = now()
			 WHERE id = $1 AND user_id = $2
			RETURNING `+memoryColumns+`
		`, id, userID, content)
		var err error
		m, err = scanMemory(row)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Memory{}, ErrNotFound
		}
		return Memory{}, fmt.Errorf("usermemories: update: %w", err)
	}
	return m, nil
}

func (r *pgxRepository) Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM public.user_memories
			 WHERE id = $1 AND user_id = $2
		`, id, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("usermemories: delete: %w", err)
	}
	return nil
}

func (r *pgxRepository) EvictOldest(ctx context.Context, tenantID, userID uuid.UUID, keep int) (int64, error) {
	var evicted int64
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM public.user_memories
			 WHERE id IN (
				SELECT id
				  FROM public.user_memories
				 WHERE user_id = $1
				 ORDER BY created_at DESC, id DESC
				 OFFSET $2
			 )
		`, userID, keep)
		if err != nil {
			return err
		}
		evicted = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("usermemories: evict oldest: %w", err)
	}
	return evicted, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMemory(s scanner) (Memory, error) {
	var m Memory
	if err := s.Scan(&m.ID, &m.TenantID, &m.UserID, &m.Content, &m.SourceChatID, &m.CreatedAt, &m.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Memory{}, ErrNotFound
		}
		return Memory{}, fmt.Errorf("usermemories: scan: %w", err)
	}
	return m, nil
}
