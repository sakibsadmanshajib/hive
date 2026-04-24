package budgets

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository creates a new pgx-backed ThresholdRepository.
func NewPgxRepository(pool *pgxpool.Pool) ThresholdRepository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) GetThreshold(ctx context.Context, accountID uuid.UUID) (*BudgetThreshold, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, threshold_credits, last_notified_at, alert_dismissed, created_at, updated_at
		FROM public.account_budget_thresholds
		WHERE account_id = $1
		LIMIT 1
	`, accountID)

	t, err := scanThreshold(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *pgxRepository) UpsertThreshold(ctx context.Context, accountID uuid.UUID, credits int64) (*BudgetThreshold, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.account_budget_thresholds (account_id, threshold_credits)
		VALUES ($1, $2)
		ON CONFLICT (account_id) DO UPDATE
		  SET threshold_credits = $2,
		      alert_dismissed = false,
		      updated_at = NOW()
		RETURNING id, account_id, threshold_credits, last_notified_at, alert_dismissed, created_at, updated_at
	`, accountID, credits)

	t, err := scanThreshold(row)
	if err != nil {
		return nil, fmt.Errorf("budgets: upsert threshold: %w", err)
	}
	return &t, nil
}

func (r *pgxRepository) DismissAlert(ctx context.Context, accountID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.account_budget_thresholds
		SET alert_dismissed = true, updated_at = NOW()
		WHERE account_id = $1
	`, accountID)
	if err != nil {
		return fmt.Errorf("budgets: dismiss alert: %w", err)
	}
	return nil
}

func (r *pgxRepository) MarkNotified(ctx context.Context, accountID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.account_budget_thresholds
		SET last_notified_at = NOW(), updated_at = NOW()
		WHERE account_id = $1
	`, accountID)
	if err != nil {
		return fmt.Errorf("budgets: mark notified: %w", err)
	}
	return nil
}

type thresholdScanner interface {
	Scan(dest ...any) error
}

func scanThreshold(scanner thresholdScanner) (BudgetThreshold, error) {
	var t BudgetThreshold
	if err := scanner.Scan(
		&t.ID,
		&t.AccountID,
		&t.ThresholdCredits,
		&t.LastNotifiedAt,
		&t.AlertDismissed,
		&t.CreatedAt,
		&t.UpdatedAt,
	); err != nil {
		return BudgetThreshold{}, fmt.Errorf("budgets: scan threshold: %w", err)
	}
	return t, nil
}
