package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
)

// ReservationHold is the ledger hold that must land in the same transaction as
// the reservation row it authorizes (issue #918). Passing it into
// CreateReservation rather than posting it separately afterwards is what makes
// "a reservation row exists if and only if its hold exists" a database
// guarantee instead of an ordering convention.
type ReservationHold struct {
	IdempotencyKey string
	Credits        int64
	Metadata       map[string]any
}

type Repository interface {
	CreateReservation(ctx context.Context, reservation Reservation, reason string, hold ReservationHold) (Reservation, error)
	GetReservation(ctx context.Context, accountID, reservationID uuid.UUID) (Reservation, error)
	ExpandReservation(ctx context.Context, accountID, reservationID uuid.UUID, additionalCredits int64, reason string, hold ReservationHold) (Reservation, error)
	FinalizeReservation(ctx context.Context, accountID, reservationID uuid.UUID, consumedCredits, releasedCredits int64, terminalUsageConfirmed bool, status ReservationStatus, reason string) (Reservation, error)
	ReleaseReservation(ctx context.Context, accountID, reservationID uuid.UUID, releasedCredits int64, reason string) (Reservation, error)
	CreateReconciliationJob(ctx context.Context, reservationID, requestAttemptID uuid.UUID, reason string) error
	ListStaleReservations(ctx context.Context, olderThan time.Time, limit int) ([]Reservation, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) CreateReservation(ctx context.Context, reservation Reservation, reason string, hold ReservationHold) (Reservation, error) {
	// Boundary guard, before any database work. A zero-value hold would post a
	// 0-credit entry under an empty idempotency key and commit, leaving a row
	// that looks authorized against a ledger holding nothing: the very state
	// this signature exists to prevent. The service validates its own input,
	// but the next caller of this method is not bound by that.
	if hold.Credits <= 0 {
		return Reservation{}, fmt.Errorf("accounting: reservation hold must be greater than zero, got %d", hold.Credits)
	}
	if strings.TrimSpace(hold.IdempotencyKey) == "" {
		return Reservation{}, fmt.Errorf("accounting: reservation hold requires an idempotency key")
	}

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

	// The hold goes in THIS transaction (issue #918). It used to be posted by
	// the service afterwards, through the ledger's own transaction, so a hold
	// that failed to post left a committed reservation row claiming credits the
	// ledger never held; the reaper then released that row and credited the
	// account for credits it never had taken. Ten production reservations, 25000
	// credits, are in that state. Here a failed hold rolls the row back with it.
	if err := postReservationHold(ctx, tx, created.AccountID, created.ID, created.RequestAttemptID, reservation.RequestID, hold); err != nil {
		return Reservation{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("accounting: commit create reservation tx: %w", err)
	}

	return created, nil
}

// postReservationHold writes one hold inside the caller's transaction and
// refuses to accept a deduplicated entry that belongs to a DIFFERENT
// reservation.
//
// PostEntryTx returns the stored entry when the idempotency key has already
// been used, which is right for a replay and wrong here: an entry carrying
// another reservation's id means this row would commit with no hold of its own,
// the exact state issue #918 exists to eliminate, and the reaper would later
// release it against nothing. Unreachable from the service today, which derives
// the key from a freshly generated reservation id, but Repository is exported
// and its contract promises the row and its hold exist together
// unconditionally.
func postReservationHold(ctx context.Context, tx pgx.Tx, accountID, reservationID, attemptID uuid.UUID, requestID string, hold ReservationHold) error {
	entry, err := ledger.PostEntryTx(ctx, tx, accountID, ledger.PostEntryInput{
		EntryType:      ledger.EntryTypeReservationHold,
		CreditsDelta:   -hold.Credits,
		IdempotencyKey: hold.IdempotencyKey,
		RequestID:      requestID,
		AttemptID:      &attemptID,
		ReservationID:  &reservationID,
		Metadata:       hold.Metadata,
	})
	if err != nil {
		return fmt.Errorf("accounting: post reservation hold: %w", err)
	}
	if entry.ReservationID == nil || *entry.ReservationID != reservationID {
		return fmt.Errorf("accounting: post reservation hold: idempotency key %q already holds credits for another reservation", hold.IdempotencyKey)
	}
	return nil
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

func (r *pgxRepository) ExpandReservation(ctx context.Context, accountID, reservationID uuid.UUID, additionalCredits int64, reason string, hold ReservationHold) (Reservation, error) {
	if hold.Credits <= 0 {
		return Reservation{}, fmt.Errorf("accounting: expansion hold must be greater than zero, got %d", hold.Credits)
	}
	if strings.TrimSpace(hold.IdempotencyKey) == "" {
		return Reservation{}, fmt.Errorf("accounting: expansion hold requires an idempotency key")
	}

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

	expanded, err := scanReservationCore(row)
	if err != nil {
		return Reservation{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.credit_reservation_events
			(reservation_id, event_type, credits_delta, reason, metadata)
		VALUES ($1, 'expanded', $2, $3, $4::jsonb)
	`, reservationID, additionalCredits, reason, metadata); err != nil {
		return Reservation{}, fmt.Errorf("accounting: insert expanded event: %w", err)
	}

	// Same transaction as the raised reserved_credits, for the same reason the
	// create path posts its hold here (issue #918), and it matters more on this
	// path: settlement releases what the ROW says is held, so a row raised
	// without its matching ledger hold hands back credits that were never taken.
	if err := postReservationHold(ctx, tx, accountID, reservationID, expanded.RequestAttemptID, expanded.RequestID, hold); err != nil {
		return Reservation{}, err
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

// ListStaleReservations returns holds still sitting in a non-terminal state
// whose reservation has not been touched since olderThan, which is how a
// request that died before settlement looks from the database (issue #616).
//
// Two predicates keep an in-flight request out of the result. Both created_at
// and updated_at must precede the cutoff, so a long request that expanded its
// hold recently is not treated as abandoned. And a reservation still attached
// to a running batch is excluded outright: a batch legitimately holds credits
// for its whole completion window, 24h by default, which is far longer than
// any inference request can run, so the batch row is the authoritative
// in-flight signal on that path rather than elapsed time.
func (r *pgxRepository) ListStaleReservations(ctx context.Context, olderThan time.Time, limit int) ([]Reservation, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.pool.Query(ctx, reservationSelect+`
		WHERE cr.status IN ('active', 'expanded')
		  AND cr.created_at < $1
		  AND cr.updated_at < $1
		  AND NOT EXISTS (
			SELECT 1
			FROM public.batches b
			WHERE b.reservation_id = cr.id::text
			  AND b.status NOT IN ('completed', 'failed', 'cancelled', 'expired')
		  )
		ORDER BY cr.created_at
		LIMIT $2
	`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("accounting: list stale reservations: %w", err)
	}
	defer rows.Close()

	var stale []Reservation
	for rows.Next() {
		reservation, err := scanReservation(rows)
		if err != nil {
			return nil, err
		}
		stale = append(stale, reservation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("accounting: iterate stale reservations: %w", err)
	}

	return stale, nil
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
