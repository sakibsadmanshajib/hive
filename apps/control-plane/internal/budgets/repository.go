package budgets

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

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

// NewWorkspacePgxRepository creates a new pgx-backed WorkspaceBudgetRepository
// for the Phase 14 budgets / spend_alerts surface.
func NewWorkspacePgxRepository(pool *pgxpool.Pool) WorkspaceBudgetRepository {
	return &pgxRepository{pool: pool}
}

// =============================================================================
// Legacy ThresholdRepository methods (preserved)
// =============================================================================

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

// =============================================================================
// Phase 14 WorkspaceBudgetRepository — budgets + spend_alerts
// =============================================================================
//
// math/big policy: BIGINT columns hold subunit counts. We marshal *big.Int
// values via Int64() at the boundary. Documented assumption: BDT subunit caps
// fit int64 (max 9.2e18 paisa = ~9.2e16 BDT, far above any plausible cap).
// Tests in service_test.go assert *big.Int.Cmp behavior survives that boundary.

func (r *pgxRepository) GetBudget(ctx context.Context, workspaceID uuid.UUID) (*Budget, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT workspace_id, period_start, soft_cap_bdt_subunits, hard_cap_bdt_subunits, currency, created_at, updated_at
		FROM public.budgets
		WHERE workspace_id = $1
	`, workspaceID)

	var (
		b           Budget
		softSubunit int64
		hardSubunit int64
	)
	if err := row.Scan(&b.WorkspaceID, &b.PeriodStart, &softSubunit, &hardSubunit, &b.Currency, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("budgets: scan budget: %w", err)
	}
	b.SoftCap = big.NewInt(softSubunit)
	b.HardCap = big.NewInt(hardSubunit)
	return &b, nil
}

func (r *pgxRepository) UpsertBudget(ctx context.Context, in SetBudgetInput) (*Budget, error) {
	if in.SoftCap == nil || in.HardCap == nil {
		return nil, fmt.Errorf("budgets: caps must be non-nil")
	}
	if !in.SoftCap.IsInt64() || !in.HardCap.IsInt64() {
		return nil, fmt.Errorf("budgets: caps overflow int64")
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.budgets (workspace_id, period_start, soft_cap_bdt_subunits, hard_cap_bdt_subunits, currency)
		VALUES ($1, $2, $3, $4, 'BDT')
		ON CONFLICT (workspace_id) DO UPDATE
		  SET period_start = $2,
		      soft_cap_bdt_subunits = $3,
		      hard_cap_bdt_subunits = $4,
		      updated_at = NOW()
		RETURNING workspace_id, period_start, soft_cap_bdt_subunits, hard_cap_bdt_subunits, currency, created_at, updated_at
	`, in.WorkspaceID, in.PeriodStart, in.SoftCap.Int64(), in.HardCap.Int64())

	var (
		b           Budget
		softSubunit int64
		hardSubunit int64
	)
	if err := row.Scan(&b.WorkspaceID, &b.PeriodStart, &softSubunit, &hardSubunit, &b.Currency, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, fmt.Errorf("budgets: upsert budget: %w", err)
	}
	b.SoftCap = big.NewInt(softSubunit)
	b.HardCap = big.NewInt(hardSubunit)
	return &b, nil
}

func (r *pgxRepository) DeleteBudget(ctx context.Context, workspaceID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM public.budgets WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return fmt.Errorf("budgets: delete budget: %w", err)
	}
	return nil
}

func (r *pgxRepository) ListAlerts(ctx context.Context, workspaceID uuid.UUID) ([]SpendAlert, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, workspace_id, threshold_pct, email, webhook_url, webhook_secret, last_fired_at, last_fired_period, created_at
		FROM public.spend_alerts
		WHERE workspace_id = $1
		ORDER BY threshold_pct ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("budgets: list alerts: %w", err)
	}
	defer rows.Close()

	var out []SpendAlert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("budgets: rows err: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) CreateAlert(ctx context.Context, in CreateAlertInput) (*SpendAlert, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.spend_alerts (workspace_id, threshold_pct, email, webhook_url, webhook_secret)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, threshold_pct) DO UPDATE
		  SET email = EXCLUDED.email,
		      webhook_url = EXCLUDED.webhook_url,
		      webhook_secret = EXCLUDED.webhook_secret
		RETURNING id, workspace_id, threshold_pct, email, webhook_url, webhook_secret, last_fired_at, last_fired_period, created_at
	`, in.WorkspaceID, in.ThresholdPct, in.Email, in.WebhookURL, in.WebhookSecret)

	a, err := scanAlert(row)
	if err != nil {
		return nil, fmt.Errorf("budgets: create alert: %w", err)
	}
	return &a, nil
}

func (r *pgxRepository) UpdateAlert(ctx context.Context, in UpdateAlertInput) (*SpendAlert, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE public.spend_alerts
		SET email = COALESCE($2, email),
		    webhook_url = COALESCE($3, webhook_url),
		    webhook_secret = COALESCE($4, webhook_secret)
		WHERE id = $1
		RETURNING id, workspace_id, threshold_pct, email, webhook_url, webhook_secret, last_fired_at, last_fired_period, created_at
	`, in.ID, in.Email, in.WebhookURL, in.WebhookSecret)

	a, err := scanAlert(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBudgetNotFound
		}
		return nil, fmt.Errorf("budgets: update alert: %w", err)
	}
	return &a, nil
}

func (r *pgxRepository) DeleteAlert(ctx context.Context, alertID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM public.spend_alerts WHERE id = $1`, alertID)
	if err != nil {
		return fmt.Errorf("budgets: delete alert: %w", err)
	}
	return nil
}

func (r *pgxRepository) ListWorkspacesWithBudget(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT workspace_id FROM public.budgets`)
	if err != nil {
		return nil, fmt.Errorf("budgets: list workspaces: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("budgets: scan workspace id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *pgxRepository) StampAlertFired(ctx context.Context, alertID uuid.UUID, firedAt time.Time, period time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.spend_alerts
		SET last_fired_at = $2, last_fired_period = $3
		WHERE id = $1
	`, alertID, firedAt, period)
	if err != nil {
		return fmt.Errorf("budgets: stamp alert fired: %w", err)
	}
	return nil
}

// MonthToDateSpendBDT sums usage_charge ledger entries since periodStart.
// credits_delta is stored as a negative integer for charges; we negate to a
// positive BDT subunit count.
//
// NOTE on units: v1.0 ledger stores Hive credits, where 1 credit ≈ 1 BDT
// subunit at the platform-fixed conversion rate (FX-clean for customer
// surface). The Phase 17 ledger refactor will collapse credits → BDT-subunits
// directly; for v1.1 we treat credits as the BDT-subunit unit. Documented in
// 14-AUDIT Section A.6.
func (r *pgxRepository) MonthToDateSpendBDT(ctx context.Context, workspaceID uuid.UUID, periodStart time.Time) (*big.Int, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(-credits_delta), 0)::bigint
		FROM public.credit_ledger_entries
		WHERE account_id = $1
		  AND entry_type = 'usage_charge'
		  AND created_at >= $2
	`, workspaceID, periodStart)

	var subunits int64
	if err := row.Scan(&subunits); err != nil {
		return nil, fmt.Errorf("budgets: scan mtd spend: %w", err)
	}
	if subunits < 0 {
		// guard against future ledger refactor surprises
		subunits = 0
	}
	return big.NewInt(subunits), nil
}

func scanAlert(scanner thresholdScanner) (SpendAlert, error) {
	var a SpendAlert
	if err := scanner.Scan(
		&a.ID,
		&a.WorkspaceID,
		&a.ThresholdPct,
		&a.Email,
		&a.WebhookURL,
		&a.WebhookSecret,
		&a.LastFiredAt,
		&a.LastFiredPeriod,
		&a.CreatedAt,
	); err != nil {
		return SpendAlert{}, fmt.Errorf("budgets: scan alert: %w", err)
	}
	return a, nil
}
