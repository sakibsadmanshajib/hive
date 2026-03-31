package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	CreateReservation(ctx context.Context, reservation Reservation, reason string) (Reservation, error)
	GetReservation(ctx context.Context, accountID, reservationID uuid.UUID) (Reservation, error)
	ExpandReservation(ctx context.Context, accountID, reservationID uuid.UUID, additionalCredits int64, reason string) (Reservation, error)
	FinalizeReservation(ctx context.Context, accountID, reservationID uuid.UUID, consumedCredits, releasedCredits int64, terminalUsageConfirmed bool, status ReservationStatus, reason string) (Reservation, error)
	ReleaseReservation(ctx context.Context, accountID, reservationID uuid.UUID, releasedCredits int64, reason string) (Reservation, error)
	CreateReconciliationJob(ctx context.Context, reservationID, requestAttemptID uuid.UUID, reason string) error
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) CreateReservation(ctx context.Context, reservation Reservation, reason string) (Reservation, error) {
	metadata, err := json.Marshal(map[string]any{
		"policy_mode":    reservation.PolicyMode,
		"request_id":     reservation.RequestID,
		"attempt_number": reservation.AttemptNumber,
	})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: marshal reservation metadata: %w", err)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: begin create reservation tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		INSERT INTO public.credit_reservations
			(id, account_id, request_attempt_id, reservation_key, policy_mode, status, reserved_credits, consumed_credits, released_credits, terminal_usage_confirmed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0, false)
		RETURNING id, account_id, request_attempt_id, reservation_key, policy_mode, status, reserved_credits, consumed_credits, released_credits, terminal_usage_confirmed, created_at, updated_at
	`, reservation.ID, reservation.AccountID, reservation.RequestAttemptID, reservation.ReservationKey, string(reservation.PolicyMode), string(reservation.Status), reservation.ReservedCredits)

	created, err := scanReservationCore(row)
	if err != nil {
		return Reservation{}, err
	}
	created.RequestID = reservation.RequestID
	created.AttemptNumber = reservation.AttemptNumber
	created.Endpoint = reservation.Endpoint
	created.ModelAlias = reservation.ModelAlias
	created.CustomerTags = normalizeJSONMap(reservation.CustomerTags)

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.credit_reservation_events
			(reservation_id, event_type, credits_delta, reason, metadata)
		VALUES ($1, 'reserved', $2, $3, $4::jsonb)
	`, created.ID, created.ReservedCredits, reason, metadata); err != nil {
		return Reservation{}, fmt.Errorf("accounting: insert reserved event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("accounting: commit create reservation tx: %w", err)
	}

	return created, nil
}

func (r *pgxRepository) GetReservation(ctx context.Context, accountID, reservationID uuid.UUID) (Reservation, error) {
	row := r.pool.QueryRow(ctx, reservationSelect+`
		WHERE cr.account_id = $1 AND cr.id = $2
	`, accountID, reservationID)

	reservation, err := scanReservation(row)
	if err != nil {
		return Reservation{}, err
	}

	return reservation, nil
}

func (r *pgxRepository) ExpandReservation(ctx context.Context, accountID, reservationID uuid.UUID, additionalCredits int64, reason string) (Reservation, error) {
	metadata, err := json.Marshal(map[string]any{"additional_credits": additionalCredits})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: marshal expand metadata: %w", err)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: begin expand reservation tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		UPDATE public.credit_reservations
		SET reserved_credits = reserved_credits + $3,
			status = 'expanded',
			updated_at = now()
		WHERE account_id = $1 AND id = $2
		RETURNING id, account_id, request_attempt_id, reservation_key, policy_mode, status, reserved_credits, consumed_credits, released_credits, terminal_usage_confirmed, created_at, updated_at
	`, accountID, reservationID, additionalCredits)

	if _, err := scanReservationCore(row); err != nil {
		return Reservation{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.credit_reservation_events
			(reservation_id, event_type, credits_delta, reason, metadata)
		VALUES ($1, 'expanded', $2, $3, $4::jsonb)
	`, reservationID, additionalCredits, reason, metadata); err != nil {
		return Reservation{}, fmt.Errorf("accounting: insert expanded event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("accounting: commit expand reservation tx: %w", err)
	}

	return r.GetReservation(ctx, accountID, reservationID)
}

func (r *pgxRepository) FinalizeReservation(ctx context.Context, accountID, reservationID uuid.UUID, consumedCredits, releasedCredits int64, terminalUsageConfirmed bool, status ReservationStatus, reason string) (Reservation, error) {
	metadata, err := json.Marshal(map[string]any{
		"consumed_credits":         consumedCredits,
		"released_credits":         releasedCredits,
		"terminal_usage_confirmed": terminalUsageConfirmed,
	})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: marshal finalize metadata: %w", err)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: begin finalize reservation tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		UPDATE public.credit_reservations
		SET consumed_credits = $3,
			released_credits = $4,
			terminal_usage_confirmed = $5,
			status = $6,
			updated_at = now()
		WHERE account_id = $1 AND id = $2
		RETURNING id, account_id, request_attempt_id, reservation_key, policy_mode, status, reserved_credits, consumed_credits, released_credits, terminal_usage_confirmed, created_at, updated_at
	`, accountID, reservationID, consumedCredits, releasedCredits, terminalUsageConfirmed, string(status))

	if _, err := scanReservationCore(row); err != nil {
		return Reservation{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.credit_reservation_events
			(reservation_id, event_type, credits_delta, reason, metadata)
		VALUES ($1, 'finalized', $2, $3, $4::jsonb)
	`, reservationID, consumedCredits, reason, metadata); err != nil {
		return Reservation{}, fmt.Errorf("accounting: insert finalized event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("accounting: commit finalize reservation tx: %w", err)
	}

	return r.GetReservation(ctx, accountID, reservationID)
}

func (r *pgxRepository) ReleaseReservation(ctx context.Context, accountID, reservationID uuid.UUID, releasedCredits int64, reason string) (Reservation, error) {
	metadata, err := json.Marshal(map[string]any{"released_credits": releasedCredits})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: marshal release metadata: %w", err)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: begin release reservation tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		UPDATE public.credit_reservations
		SET released_credits = $3,
			status = 'released',
			updated_at = now()
		WHERE account_id = $1 AND id = $2 AND status <> 'released'
		RETURNING id, account_id, request_attempt_id, reservation_key, policy_mode, status, reserved_credits, consumed_credits, released_credits, terminal_usage_confirmed, created_at, updated_at
	`, accountID, reservationID, releasedCredits)

	if _, err := scanReservationCore(row); err != nil {
		if errors.Is(err, ErrNotFound) {
			existing, lookupErr := r.getReservationTx(ctx, tx, accountID, reservationID)
			if lookupErr != nil {
				return Reservation{}, lookupErr
			}
			if err := tx.Commit(ctx); err != nil {
				return Reservation{}, fmt.Errorf("accounting: commit duplicate release tx: %w", err)
			}
			return existing, nil
		}
		return Reservation{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.credit_reservation_events
			(reservation_id, event_type, credits_delta, reason, metadata)
		VALUES ($1, 'released', $2, $3, $4::jsonb)
	`, reservationID, releasedCredits, reason, metadata); err != nil {
		return Reservation{}, fmt.Errorf("accounting: insert released event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("accounting: commit release reservation tx: %w", err)
	}

	return r.GetReservation(ctx, accountID, reservationID)
}

func (r *pgxRepository) CreateReconciliationJob(ctx context.Context, reservationID, requestAttemptID uuid.UUID, reason string) error {
	metadata, err := json.Marshal(map[string]any{"reason": reason})
	if err != nil {
		return fmt.Errorf("accounting: marshal reconciliation metadata: %w", err)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("accounting: begin reconciliation tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.credit_reconciliation_jobs
			(reservation_id, request_attempt_id, reason)
		VALUES ($1, $2, $3)
	`, reservationID, requestAttemptID, reason); err != nil {
		return fmt.Errorf("accounting: insert reconciliation job: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.credit_reservation_events
			(reservation_id, event_type, credits_delta, reason, metadata)
		VALUES ($1, 'marked_for_reconciliation', 0, $2, $3::jsonb)
	`, reservationID, reason, metadata); err != nil {
		return fmt.Errorf("accounting: insert reconciliation event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("accounting: commit reconciliation tx: %w", err)
	}

	return nil
}

const reservationSelect = `
	SELECT
		cr.id,
		cr.account_id,
		cr.request_attempt_id,
		cr.reservation_key,
		cr.policy_mode,
		cr.status,
		cr.reserved_credits,
		cr.consumed_credits,
		cr.released_credits,
		cr.terminal_usage_confirmed,
		ra.request_id,
		ra.attempt_number,
		ra.endpoint,
		ra.model_alias,
		ra.customer_tags,
		cr.created_at,
		cr.updated_at
	FROM public.credit_reservations cr
	JOIN public.request_attempts ra ON ra.id = cr.request_attempt_id
`

type reservationScanner interface {
	Scan(dest ...any) error
}

func (r *pgxRepository) getReservationTx(ctx context.Context, tx pgx.Tx, accountID, reservationID uuid.UUID) (Reservation, error) {
	row := tx.QueryRow(ctx, reservationSelect+`
		WHERE cr.account_id = $1 AND cr.id = $2
	`, accountID, reservationID)
	return scanReservation(row)
}

func scanReservation(scanner reservationScanner) (Reservation, error) {
	var reservation Reservation
	var customerTags []byte
	if err := scanner.Scan(
		&reservation.ID,
		&reservation.AccountID,
		&reservation.RequestAttemptID,
		&reservation.ReservationKey,
		&reservation.PolicyMode,
		&reservation.Status,
		&reservation.ReservedCredits,
		&reservation.ConsumedCredits,
		&reservation.ReleasedCredits,
		&reservation.TerminalUsageConfirmed,
		&reservation.RequestID,
		&reservation.AttemptNumber,
		&reservation.Endpoint,
		&reservation.ModelAlias,
		&customerTags,
		&reservation.CreatedAt,
		&reservation.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reservation{}, ErrNotFound
		}
		return Reservation{}, fmt.Errorf("accounting: scan reservation: %w", err)
	}

	reservation.CustomerTags = map[string]any{}
	if len(customerTags) > 0 {
		if err := json.Unmarshal(customerTags, &reservation.CustomerTags); err != nil {
			return Reservation{}, fmt.Errorf("accounting: decode customer tags: %w", err)
		}
	}

	return reservation, nil
}

func scanReservationCore(scanner reservationScanner) (Reservation, error) {
	var reservation Reservation
	if err := scanner.Scan(
		&reservation.ID,
		&reservation.AccountID,
		&reservation.RequestAttemptID,
		&reservation.ReservationKey,
		&reservation.PolicyMode,
		&reservation.Status,
		&reservation.ReservedCredits,
		&reservation.ConsumedCredits,
		&reservation.ReleasedCredits,
		&reservation.TerminalUsageConfirmed,
		&reservation.CreatedAt,
		&reservation.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reservation{}, ErrNotFound
		}
		return Reservation{}, fmt.Errorf("accounting: scan reservation core: %w", err)
	}

	reservation.CustomerTags = map[string]any{}
	return reservation, nil
}

func normalizeJSONMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	return input
}
