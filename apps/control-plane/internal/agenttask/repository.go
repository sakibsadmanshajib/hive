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
	Create(ctx context.Context, tenantID, userID uuid.UUID, pack Pack, instructions string, projectID uuid.UUID) (Task, error)
	Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error)
	List(ctx context.Context, tenantID, userID uuid.UUID) ([]Task, error)
	// Transition updates status (and the fields that go with it) for a task
	// already scoped to (tenantID, userID) by the caller.
	Transition(ctx context.Context, tenantID, userID, id uuid.UUID, status Status, sessionRef, resultSummaryRef, errMsg string) (Task, error)
	// ListActive returns every task across all tenants that is queued or
	// running with a non-empty EngineSessionRef (i.e. actually launched
	// somewhere) — the Poller's input. Cross-tenant by design; see
	// 20260716_05_agent_tasks_service_scan.sql.
	ListActive(ctx context.Context) ([]Task, error)
	// AppendEvents inserts events for one task inside withTenantTx, assigning
	// per-task monotonic seq values continuing after the current maximum.
	// Rows whose (task_id, source_event_id) already exist are skipped via the
	// dedup partial unique index; everything else in the batch commits.
	// task must be the row AppendEvents scopes the writes to (its ID and
	// TenantID drive the transaction).
	AppendEvents(ctx context.Context, task Task, events []TaskEvent) error
	// ListEvents returns at most limit events of one task with seq >
	// afterSeq, ordered by seq ascending — exactly what the internal and
	// customer read routes serve. Caller-scoped: Get(tenantID, userID, id)
	// must have succeeded first, since user scoping stays at the application
	// layer.
	ListEvents(ctx context.Context, tenantID, userID, id uuid.UUID, afterSeq int64, limit int) ([]TaskEvent, error)
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

func (r *pgxRepository) Create(ctx context.Context, tenantID, userID uuid.UUID, pack Pack, instructions string, projectID uuid.UUID) (Task, error) {
	var t Task
	// An absent project travels as SQL NULL. Written but never read back here:
	// project_id is deliberately left off every SELECT in this file, because
	// ListActive reads public.agent_tasks_list_active(), a SECURITY DEFINER
	// function with a fixed RETURNS TABLE signature, so widening the shared
	// scanTask column list would need that function's signature changed in the
	// same breath. The column records which project a run was submitted with;
	// the retrieve-before-launch half that consumes it is issue #1595 spec task
	// 9 and is not in this change.
	var project any
	if projectID != uuid.Nil {
		project = projectID
	}
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO public.agent_tasks (tenant_id, user_id, pack, instructions, project_id)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5)
			RETURNING id, tenant_id, user_id, pack, COALESCE(instructions, ''), status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
		`, tenantID, userID, string(pack), instructions, project)
		var err error
		t, err = scanTask(row)
		return err
	})
	if err != nil {
		return Task{}, fmt.Errorf("agenttask: create: %w", err)
	}
	// Carried on the in-memory Task for the launch path, the same way BearerJWT
	// is. Repository never reads it back, so a Task loaded later has it Nil.
	t.ProjectID = projectID
	return t, nil
}
func (r *pgxRepository) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error) {
	var t Task
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, tenant_id, user_id, pack, COALESCE(instructions, ''), status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
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
			SELECT id, tenant_id, user_id, pack, COALESCE(instructions, ''), status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
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
		// tenantID is not a bind parameter here (tenant scoping is enforced
		// solely by RLS via the app.current_tenant_id session variable
		// withTenantTx already sets, same as Get/List): pgx's extended query
		// protocol cannot infer a type for a parameter the query text never
		// references, and fails closed with "could not determine data type
		// of parameter" (SQLSTATE 42P18) rather than silently ignoring it.
		row := tx.QueryRow(ctx, `
			UPDATE public.agent_tasks
			   SET status = $3,
			       engine_session_ref = CASE WHEN $4 <> '' THEN $4 ELSE engine_session_ref END,
			       result_summary_ref = CASE WHEN $5 <> '' THEN $5 ELSE result_summary_ref END,
			       error_message = $6,
			       started_at = CASE WHEN started_at IS NULL AND $3 = 'running' THEN now() ELSE started_at END,
			       finished_at = CASE WHEN finished_at IS NULL AND $3 IN ('succeeded', 'failed', 'cancelled') THEN now() ELSE finished_at END,
			       updated_at = now()
			 WHERE id = $1 AND user_id = $2
			   AND status NOT IN ('succeeded', 'failed', 'cancelled')
			RETURNING id, tenant_id, user_id, pack, COALESCE(instructions, ''), status, engine_session_ref, result_summary_ref, error_message, created_at, updated_at, started_at, finished_at
		`, id, userID, string(status), sessionRef, resultSummaryRef, errMsg)
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

// ListActive calls public.agent_tasks_list_active() — a SECURITY DEFINER
// function (20260716_05_agent_tasks_service_scan.sql), NOT a table policy:
// this is deliberately the only cross-tenant read path against
// public.agent_tasks. An earlier version of this migration used a blanket
// PERMISSIVE SELECT policy instead, which Postgres OR-combines with
// agent_tasks_tenant_isolation and would have cancelled it for every
// hive_app SELECT (a cross-tenant leak on the ordinary Get/List path) — see
// the migration's own comment for the full account. Called outside
// withTenantTx: no single app.current_tenant_id value could see every
// tenant's rows, which is exactly why the function exists.
func (r *pgxRepository) ListActive(ctx context.Context) ([]Task, error) {
	rows, err := r.pool.Query(ctx, `SELECT * FROM public.agent_tasks_list_active()`)
	if err != nil {
		return nil, fmt.Errorf("agenttask: list active query: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agenttask: list active: %w", err)
	}
	return out, nil
}

// AppendEvents assigns per-task monotonic seq inside the same transaction
// that inserts, then writes each row with ON CONFLICT DO NOTHING against the
// dedup partial unique index. Two deliberate properties:
//
//   - seq is computed from COALESCE(MAX(seq), 0) + i inside the tx. The tx
//     first takes pg_advisory_xact_lock(hashtextextended(task_id)) so two
//     concurrent writers for the same task (a future second replica) cannot
//     compute the same base and race UNIQUE(task_id, seq) -- a constraint the
//     ON CONFLICT clause does not cover, whose loser would abort its whole
//     batch on every retry while both writers stayed live. The lock is
//     per-task, transaction-scoped, released at commit, so it serializes only
//     same-task appends and nothing else waits.
//   - ON CONFLICT (task_id, source_event_id) WHERE source_event_id <> ”
//     targets the partial unique index exactly, so a re-pulled event is a
//     no-op while genuinely new events in the same batch still land.
func (r *pgxRepository) AppendEvents(ctx context.Context, task Task, events []TaskEvent) error {
	if len(events) == 0 {
		return nil
	}
	return r.withTenantTx(ctx, task.TenantID, func(tx pgx.Tx) error {
		// Serialize per-task seq allocation before the base read; see the doc
		// comment above.
		if _, err := tx.Exec(ctx,
			"SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))",
			task.ID.String()); err != nil {
			return fmt.Errorf("agenttask: append events lock: %w", err)
		}
		var base int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(seq), 0) FROM public.agent_task_events WHERE task_id = $1`,
			task.ID).Scan(&base); err != nil {
			return fmt.Errorf("agenttask: append events base seq: %w", err)
		}
		for i, ev := range events {
			if _, err := tx.Exec(ctx, `
				INSERT INTO public.agent_task_events (task_id, seq, source_event_id, kind, payload)
				VALUES ($1, $2, $3, $4, COALESCE($5, '{}'::jsonb))
				ON CONFLICT (task_id, source_event_id) WHERE source_event_id <> '' DO NOTHING
			`, task.ID, base+int64(i)+1, ev.SourceEventID, string(ev.Kind), []byte(ev.Payload)); err != nil {
				return fmt.Errorf("agenttask: append event %d: %w", i, err)
			}
		}
		return nil
	})
}

// ListEvents serves the cursor read: strictly newer rows in seq order.
// User scoping follows the package's application-layer pattern: an explicit
// user_id filter on every read (here an EXISTS against the parent task), so a
// same-tenant cross-user read returns nothing even if a future caller skips
// the Service's Get pre-check.
func (r *pgxRepository) ListEvents(ctx context.Context, tenantID, userID, id uuid.UUID, afterSeq int64, limit int) ([]TaskEvent, error) {
	var out []TaskEvent
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT e.seq, e.source_event_id, e.kind, e.payload, e.created_at
			  FROM public.agent_task_events e
			 WHERE e.task_id = $1
			   AND e.seq > $2
			   AND EXISTS (SELECT 1 FROM public.agent_tasks at
			                WHERE at.id = e.task_id AND at.user_id = $3)
			 ORDER BY e.seq ASC
			 LIMIT $4
		`, id, afterSeq, userID, limit)
		if err != nil {
			return fmt.Errorf("agenttask: list events query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var ev TaskEvent
			var kind string
			if err := rows.Scan(&ev.Seq, &ev.SourceEventID, &kind, &ev.Payload, &ev.CreatedAt); err != nil {
				return fmt.Errorf("agenttask: scan event: %w", err)
			}
			ev.Kind = TaskEventKind(kind)
			out = append(out, ev)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("agenttask: list events: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (Task, error) {
	var t Task
	var pack, status string
	if err := s.Scan(&t.ID, &t.TenantID, &t.UserID, &pack, &t.Instructions, &status,
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
