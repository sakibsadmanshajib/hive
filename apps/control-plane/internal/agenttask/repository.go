package agenttask

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the narrow data-access port for public.agent_tasks.
type Repository interface {
	Create(ctx context.Context, tenantID, userID uuid.UUID, pack Pack) (Task, error)
	Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error)
	List(ctx context.Context, tenantID, userID uuid.UUID) ([]Task, error)
	// Transition updates status (and the fields that go with it) for a task
	// already scoped to (tenantID, userID) by the caller.
	Transition(ctx context.Context, tenantID, userID, id uuid.UUID, status Status, sessionRef, resultSummaryRef, errMsg string) (Task, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository constructs a pgxpool-backed Repository.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

// withTenantTx mirrors apps/control-plane/internal/marketplace/repository.go
// and apps/control-plane/internal/egress/repository.go's helper of the same
// name: hive_app is NOT BYPASSRLS (20260518_04_phase19_audit_rls_and_indexes.sql),
// so every query against public.agent_tasks must run inside an explicit
// transaction with app.current_tenant_id set LOCAL — guaranteed to clear at
// Commit or Rollback so nothing survives onto the pooled connection for the
// next borrower.
func (r *pgxRepository) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("agenttask: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("agenttask: set tenant: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *pgxRepository) Create(ctx context.Context, tenantID, userID uuid.UUID, pack Pack) (Task, error) {
	var t Task
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO public.agent_tasks (tenant_id, user_id, pack)
			VALUES ($1, $2, $3)
			RETURNING id, tenant_id, user_id, pack, status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
		`, tenantID, userID, string(pack))
		var err error
		t, err = scanTask(row)
		return err
	})
	if err != nil {
		return Task{}, fmt.Errorf("agenttask: create: %w", err)
	}
	return t, nil
}

func (r *pgxRepository) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error) {
	var t Task
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, tenant_id, user_id, pack, status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
			  FROM public.agent_tasks
			 WHERE id = $1 AND user_id = $2
		`, id, userID)
		var err error
		t, err = scanTask(row)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("agenttask: get: %w", err)
	}
	return t, nil
}

func (r *pgxRepository) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Task, error) {
	var out []Task
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, user_id, pack, status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
			  FROM public.agent_tasks
			 WHERE user_id = $1
			 ORDER BY created_at DESC
		`, userID)
		if err != nil {
			return fmt.Errorf("agenttask: list query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("agenttask: list: %w", err)
	}
	return out, nil
}

// Transition updates status atomically: the UPDATE itself carries a "not
// already terminal" precondition, so a status flip (e.g. an async engine
// callback landing succeeded) can never be clobbered by a concurrent Cancel
// racing against it, or vice versa — whichever transition's UPDATE commits
// first wins, and the loser's statement matches zero rows instead of
// silently overwriting a terminal state (last-write-wins was the bug: the
// old UPDATE had no status precondition at all).
func (r *pgxRepository) Transition(ctx context.Context, tenantID, userID, id uuid.UUID, status Status, sessionRef, resultSummaryRef, errMsg string) (Task, error) {
	var t Task
	var notFound, terminal bool
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE public.agent_tasks
			   SET status = $4,
			       engine_session_ref = CASE WHEN $5 <> '' THEN $5 ELSE engine_session_ref END,
			       result_summary_ref = CASE WHEN $6 <> '' THEN $6 ELSE result_summary_ref END,
			       error_message = $7,
			       started_at = CASE WHEN started_at IS NULL AND $4 = 'running' THEN now() ELSE started_at END,
			       finished_at = CASE WHEN finished_at IS NULL AND $4 IN ('succeeded', 'failed', 'cancelled') THEN now() ELSE finished_at END,
			       updated_at = now()
			 WHERE id = $1 AND user_id = $2
			   AND status NOT IN ('succeeded', 'failed', 'cancelled')
			RETURNING id, tenant_id, user_id, pack, status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
		`, id, userID, tenantID, string(status), sessionRef, resultSummaryRef, errMsg)
		var err error
		t, err = scanTask(row)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}

		// The guard blocked the UPDATE (0 rows) — disambiguate "no such
		// task for this id+user" from "task exists but is terminal" with a
		// plain read scoped the same way, in the same transaction.
		var exists bool
		qerr := tx.QueryRow(ctx,
			`SELECT true FROM public.agent_tasks WHERE id = $1 AND user_id = $2`,
			id, userID).Scan(&exists)
		switch {
		case errors.Is(qerr, pgx.ErrNoRows):
			notFound = true
			return nil
		case qerr != nil:
			return qerr
		default:
			terminal = true
			return nil
		}
	})
	if err != nil {
		return Task{}, fmt.Errorf("agenttask: transition: %w", err)
	}
	if notFound {
		return Task{}, ErrNotFound
	}
	if terminal {
		return Task{}, ErrTerminalState
	}
	return t, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (Task, error) {
	var t Task
	var pack, status string
	if err := s.Scan(&t.ID, &t.TenantID, &t.UserID, &pack, &status,
		&t.EngineSessionRef, &t.ResultSummaryRef, &t.ErrorMessage,
		&t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.FinishedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("agenttask: scan: %w", err)
	}
	t.Pack = Pack(pack)
	t.Status = Status(status)
	return t, nil
}
