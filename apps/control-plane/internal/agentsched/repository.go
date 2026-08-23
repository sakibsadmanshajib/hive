package agentsched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the narrow data-access port for
// public.agent_task_schedules. CRUD and the run-outcome writes are
// (tenant, user)-scoped; ClaimDue is the scheduler's one cross-tenant
// statement and goes through the SECURITY DEFINER function
// public.agent_task_schedules_claim_due (20260823_01_agent_task_schedules.sql),
// never a table policy.
type Repository interface {
	Create(ctx context.Context, s Schedule) (Schedule, error)
	Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Schedule, error)
	List(ctx context.Context, tenantID, userID uuid.UUID) ([]Schedule, error)
	Update(ctx context.Context, s Schedule) (Schedule, error)
	SetEnabled(ctx context.Context, tenantID, userID, id uuid.UUID, enabled bool, nextRunAt *time.Time) (Schedule, error)
	Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error
	// RecordRunSuccess records the task a claimed schedule produced. Scoped
	// to the claimed row's tenant: the scheduler holds tenantID straight off
	// the row ClaimDue returned. next_run_at was already advanced at claim
	// time; this only fills in the outcome.
	RecordRunSuccess(ctx context.Context, tenantID, id, taskID uuid.UUID) error
	// RecordRunFailure records a provider-blind failure message from the most
	// recent run attempt. next_run_at was already advanced at claim time,
	// which IS the backoff: the schedule waits out its full cadence instead of
	// hot-looping.
	RecordRunFailure(ctx context.Context, tenantID, id uuid.UUID, message string) error
	// ClaimDue atomically claims up to batch due enabled schedules as of now:
	// advances next_run_at by one cadence and returns the rows in one
	// statement, so concurrent ticks can never fire the same schedule twice.
	ClaimDue(ctx context.Context, now time.Time, batch int) ([]Schedule, error)
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
// public.agent_task_schedules must run inside an explicit transaction with
// app.current_tenant_id set LOCAL — guaranteed to clear at Commit or Rollback
// so nothing survives onto the pooled connection for the next borrower.
func (r *pgxRepository) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("agentsched: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("agentsched: set tenant: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const scheduleColumns = `id, tenant_id, user_id, name, instructions, schedule, enabled,
	next_run_at, last_run_at, last_task_id, last_error, created_at, updated_at`

func (r *pgxRepository) Create(ctx context.Context, s Schedule) (Schedule, error) {
	var out Schedule
	err := r.withTenantTx(ctx, s.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO public.agent_task_schedules
				(tenant_id, user_id, name, instructions, schedule, enabled, next_run_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+scheduleColumns+`
		`, s.TenantID, s.UserID, s.Name, s.Instructions, s.Schedule, s.Enabled, s.NextRunAt)
		var err error
		out, err = scanSchedule(row)
		return err
	})
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched: create: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Schedule, error) {
	var out Schedule
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT `+scheduleColumns+`
			  FROM public.agent_task_schedules
			 WHERE id = $1 AND user_id = $2
		`, id, userID)
		var err error
		out, err = scanSchedule(row)
		return err
	})
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched: get: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Schedule, error) {
	var out []Schedule
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+scheduleColumns+`
			  FROM public.agent_task_schedules
			 WHERE user_id = $1
			 ORDER BY created_at DESC
		`, userID)
		if err != nil {
			return fmt.Errorf("agentsched: list query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			s, err := scanSchedule(rows)
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("agentsched: list: %w", err)
	}
	return out, nil
}

// Update rewrites the mutable fields of an existing row. The UPDATE carries
// the owning user_id and the new next_run_at computed by Service, which owns
// cadence math.
func (r *pgxRepository) Update(ctx context.Context, s Schedule) (Schedule, error) {
	var out Schedule
	err := r.withTenantTx(ctx, s.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE public.agent_task_schedules
			   SET name = $3, instructions = $4, schedule = $5,
			       enabled = $6, next_run_at = $7, updated_at = now()
			 WHERE id = $1 AND user_id = $2
			RETURNING `+scheduleColumns+`
		`, s.ID, s.UserID, s.Name, s.Instructions, s.Schedule, s.Enabled, s.NextRunAt)
		var err error
		out, err = scanSchedule(row)
		return err
	})
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched: update: %w", err)
	}
	return out, nil
}

// SetEnabled flips only the enabled flag. When enabling, Service passes the
// fresh nextRunAt (now + one cadence) so a stale overdue timestamp frozen
// during a disabled stretch cannot fire immediately on re-enable; when
// disabling, a nil nextRunAt keeps the stored value untouched.
func (r *pgxRepository) SetEnabled(ctx context.Context, tenantID, userID, id uuid.UUID, enabled bool, nextRunAt *time.Time) (Schedule, error) {
	var out Schedule
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE public.agent_task_schedules
			   SET enabled = $4,
			       next_run_at = CASE WHEN $5::timestamptz IS NULL OR NOT $4 THEN next_run_at ELSE $5 END,
			       updated_at = now()
			 WHERE id = $1 AND user_id = $2
			RETURNING `+scheduleColumns+`
		`, id, userID, enabled, nextRunAt)
		var err error
		out, err = scanSchedule(row)
		return err
	})
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched: set enabled: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	var found bool
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM public.agent_task_schedules
			 WHERE id = $1 AND user_id = $2
		`, id, userID)
		if err != nil {
			return err
		}
		found = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return fmt.Errorf("agentsched: delete: %w", err)
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func (r *pgxRepository) RecordRunSuccess(ctx context.Context, tenantID, id, taskID uuid.UUID) error {
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE public.agent_task_schedules
			   SET last_task_id = $2, updated_at = now()
			 WHERE id = $1
		`, id, taskID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("agentsched: record run success: %w", err)
	}
	return nil
}

func (r *pgxRepository) RecordRunFailure(ctx context.Context, tenantID, id uuid.UUID, message string) error {
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE public.agent_task_schedules
			   SET last_error = $2, updated_at = now()
			 WHERE id = $1
		`, id, message)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("agentsched: record run failure: %w", err)
	}
	return nil
}

// ClaimDue calls public.agent_task_schedules_claim_due(p_now, p_batch) — the
// SECURITY DEFINER function that is this package's ONLY cross-tenant path
// (20260823_01_agent_task_schedules.sql), for the same reason
// agent_tasks_list_active() exists for the poller. Called outside
// withTenantTx: no single app.current_tenant_id value could see every
// tenant's rows, which is exactly why the function exists.
func (r *pgxRepository) ClaimDue(ctx context.Context, now time.Time, batch int) ([]Schedule, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT * FROM public.agent_task_schedules_claim_due($1, $2)`,
		now, batch)
	if err != nil {
		return nil, fmt.Errorf("agentsched: claim due query: %w", err)
	}
	defer rows.Close()

	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agentsched: claim due: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSchedule(s scanner) (Schedule, error) {
	var (
		out      Schedule
		schedule string
	)
	if err := s.Scan(&out.ID, &out.TenantID, &out.UserID, &out.Name, &out.Instructions, &schedule,
		&out.Enabled, &out.NextRunAt, &out.LastRunAt, &out.LastTaskID, &out.LastError,
		&out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Schedule{}, ErrNotFound
		}
		return Schedule{}, fmt.Errorf("agentsched: scan: %w", err)
	}
	out.Schedule = schedule
	return out, nil
}
