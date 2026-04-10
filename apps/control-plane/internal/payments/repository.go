package payments

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines the persistence contract for the payments domain.
type Repository interface {
	InsertPaymentIntent(ctx context.Context, intent PaymentIntent) error
	GetPaymentIntent(ctx context.Context, id uuid.UUID) (PaymentIntent, error)
	GetPaymentIntentByProviderID(ctx context.Context, providerID string) (PaymentIntent, error)
	CompareAndSetStatus(ctx context.Context, id uuid.UUID, from, to IntentStatus) (bool, error)
	UpdateProviderDetails(ctx context.Context, id uuid.UUID, providerIntentID, redirectURL string, expiresAt *time.Time) error
	SetConfirmingAt(ctx context.Context, id uuid.UUID, at time.Time) error
	ListConfirmingIntents(ctx context.Context, olderThan time.Time) ([]PaymentIntent, error)
	InsertPaymentEvent(ctx context.Context, event PaymentEvent) error
	InsertFXSnapshot(ctx context.Context, snapshot FXSnapshot) error
	GetFXSnapshot(ctx context.Context, id uuid.UUID) (FXSnapshot, error)
}

// pgxRepository is the Postgres implementation backed by pgxpool.
type pgxRepository struct {
	db *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by the given pgxpool.
func NewPgxRepository(db *pgxpool.Pool) Repository {
	return &pgxRepository{db: db}
}

func (r *pgxRepository) InsertPaymentIntent(ctx context.Context, intent PaymentIntent) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO public.payment_intents (
			id, account_id, rail, status, credits, amount_usd, amount_local,
			local_currency, fx_snapshot_id, provider_intent_id, redirect_url,
			tax_treatment, tax_rate, tax_amount_local, idempotency_key,
			confirming_at, expires_at, metadata, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20
		)`,
		intent.ID, intent.AccountID, intent.Rail, intent.Status,
		intent.Credits, intent.AmountUSD, intent.AmountLocal,
		intent.LocalCurrency, intent.FXSnapshotID, intent.ProviderIntentID,
		intent.RedirectURL, intent.TaxTreatment, intent.TaxRate,
		intent.TaxAmountLocal, intent.IdempotencyKey, intent.ConfirmingAt,
		intent.ExpiresAt, intent.Metadata, intent.CreatedAt, intent.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("payments: insert intent: %w", err)
	}
	return nil
}

func (r *pgxRepository) GetPaymentIntent(ctx context.Context, id uuid.UUID) (PaymentIntent, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, account_id, rail, status, credits, amount_usd, amount_local,
		       local_currency, fx_snapshot_id, provider_intent_id, redirect_url,
		       tax_treatment, tax_rate, tax_amount_local, idempotency_key,
		       confirming_at, expires_at, metadata, created_at, updated_at
		FROM public.payment_intents WHERE id = $1`, id)
	return scanPaymentIntent(row)
}

func (r *pgxRepository) GetPaymentIntentByProviderID(ctx context.Context, providerID string) (PaymentIntent, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, account_id, rail, status, credits, amount_usd, amount_local,
		       local_currency, fx_snapshot_id, provider_intent_id, redirect_url,
		       tax_treatment, tax_rate, tax_amount_local, idempotency_key,
		       confirming_at, expires_at, metadata, created_at, updated_at
		FROM public.payment_intents WHERE provider_intent_id = $1`, providerID)
	return scanPaymentIntent(row)
}

func (r *pgxRepository) CompareAndSetStatus(ctx context.Context, id uuid.UUID, from, to IntentStatus) (bool, error) {
	var resultID uuid.UUID
	err := r.db.QueryRow(ctx, `
		UPDATE public.payment_intents
		   SET status = $1, updated_at = now()
		 WHERE id = $2 AND status = $3
		RETURNING id`,
		to, id, from,
	).Scan(&resultID)

	if err != nil {
		if err == pgx.ErrNoRows {
			// Already transitioned — idempotent.
			return false, nil
		}
		return false, fmt.Errorf("payments: compare-and-set status: %w", err)
	}
	return true, nil
}

func (r *pgxRepository) UpdateProviderDetails(ctx context.Context, id uuid.UUID, providerIntentID, redirectURL string, expiresAt *time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE public.payment_intents
		   SET provider_intent_id = $1, redirect_url = $2, expires_at = $3, updated_at = now()
		 WHERE id = $4`,
		providerIntentID, redirectURL, expiresAt, id,
	)
	if err != nil {
		return fmt.Errorf("payments: update provider details: %w", err)
	}
	return nil
}

func (r *pgxRepository) SetConfirmingAt(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE public.payment_intents
		   SET confirming_at = $1, updated_at = now()
		 WHERE id = $2`,
		at, id,
	)
	if err != nil {
		return fmt.Errorf("payments: set confirming_at: %w", err)
	}
	return nil
}

func (r *pgxRepository) ListConfirmingIntents(ctx context.Context, olderThan time.Time) ([]PaymentIntent, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, account_id, rail, status, credits, amount_usd, amount_local,
		       local_currency, fx_snapshot_id, provider_intent_id, redirect_url,
		       tax_treatment, tax_rate, tax_amount_local, idempotency_key,
		       confirming_at, expires_at, metadata, created_at, updated_at
		FROM public.payment_intents
		WHERE status = 'confirming' AND confirming_at <= $1`, olderThan)
	if err != nil {
		return nil, fmt.Errorf("payments: list confirming intents: %w", err)
	}
	defer rows.Close()

	var intents []PaymentIntent
	for rows.Next() {
		intent, err := scanPaymentIntentRow(rows)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (r *pgxRepository) InsertPaymentEvent(ctx context.Context, event PaymentEvent) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO public.payment_events (
			id, payment_intent_id, event_type, rail, provider_event_id, raw_payload, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		event.ID, event.PaymentIntentID, event.EventType, event.Rail,
		event.ProviderEventID, event.RawPayload, event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("payments: insert payment event: %w", err)
	}
	return nil
}

func (r *pgxRepository) InsertFXSnapshot(ctx context.Context, snap FXSnapshot) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO public.fx_snapshots (
			id, account_id, base_currency, quote_currency, mid_rate, fee_rate,
			effective_rate, source_api, fetched_at, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		snap.ID, snap.AccountID, snap.BaseCurrency, snap.QuoteCurrency,
		snap.MidRate, snap.FeeRate, snap.EffectiveRate, snap.SourceAPI,
		snap.FetchedAt, snap.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("payments: insert fx snapshot: %w", err)
	}
	return nil
}

func (r *pgxRepository) GetFXSnapshot(ctx context.Context, id uuid.UUID) (FXSnapshot, error) {
	var snap FXSnapshot
	err := r.db.QueryRow(ctx, `
		SELECT id, account_id, base_currency, quote_currency, mid_rate, fee_rate,
		       effective_rate, source_api, fetched_at, created_at
		FROM public.fx_snapshots WHERE id = $1`, id,
	).Scan(
		&snap.ID, &snap.AccountID, &snap.BaseCurrency, &snap.QuoteCurrency,
		&snap.MidRate, &snap.FeeRate, &snap.EffectiveRate, &snap.SourceAPI,
		&snap.FetchedAt, &snap.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return FXSnapshot{}, ErrIntentNotFound
		}
		return FXSnapshot{}, fmt.Errorf("payments: get fx snapshot: %w", err)
	}
	return snap, nil
}

// scanPaymentIntent scans a single pgx Row into a PaymentIntent.
func scanPaymentIntent(row pgx.Row) (PaymentIntent, error) {
	var intent PaymentIntent
	err := row.Scan(
		&intent.ID, &intent.AccountID, &intent.Rail, &intent.Status,
		&intent.Credits, &intent.AmountUSD, &intent.AmountLocal,
		&intent.LocalCurrency, &intent.FXSnapshotID, &intent.ProviderIntentID,
		&intent.RedirectURL, &intent.TaxTreatment, &intent.TaxRate,
		&intent.TaxAmountLocal, &intent.IdempotencyKey,
		&intent.ConfirmingAt, &intent.ExpiresAt, &intent.Metadata,
		&intent.CreatedAt, &intent.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return PaymentIntent{}, ErrIntentNotFound
		}
		return PaymentIntent{}, fmt.Errorf("payments: scan intent: %w", err)
	}
	return intent, nil
}

// scanPaymentIntentRow scans a pgx.Rows row (used in list queries).
type pgxRowScanner interface {
	Scan(dest ...any) error
}

func scanPaymentIntentRow(row pgxRowScanner) (PaymentIntent, error) {
	var intent PaymentIntent
	err := row.Scan(
		&intent.ID, &intent.AccountID, &intent.Rail, &intent.Status,
		&intent.Credits, &intent.AmountUSD, &intent.AmountLocal,
		&intent.LocalCurrency, &intent.FXSnapshotID, &intent.ProviderIntentID,
		&intent.RedirectURL, &intent.TaxTreatment, &intent.TaxRate,
		&intent.TaxAmountLocal, &intent.IdempotencyKey,
		&intent.ConfirmingAt, &intent.ExpiresAt, &intent.Metadata,
		&intent.CreatedAt, &intent.UpdatedAt,
	)
	if err != nil {
		return PaymentIntent{}, fmt.Errorf("payments: scan intent row: %w", err)
	}
	return intent, nil
}
