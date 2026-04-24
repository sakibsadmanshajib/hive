package ledger

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

type Repository interface {
	PostEntry(ctx context.Context, accountID uuid.UUID, input PostEntryInput) (LedgerEntry, error)
	GetBalance(ctx context.Context, accountID uuid.UUID) (BalanceSummary, error)
	ListEntries(ctx context.Context, accountID uuid.UUID, limit int) ([]LedgerEntry, error)
	ListEntriesWithCursor(ctx context.Context, filter ListEntriesFilter) ([]LedgerEntry, error)
	ListInvoices(ctx context.Context, accountID uuid.UUID) ([]InvoiceRow, error)
	GetInvoice(ctx context.Context, accountID uuid.UUID, invoiceID uuid.UUID) (*InvoiceRow, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) PostEntry(ctx context.Context, accountID uuid.UUID, input PostEntryInput) (LedgerEntry, error) {
	metadataBytes, err := json.Marshal(normalizeMetadata(input.Metadata))
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: marshal metadata: %w", err)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
		INSERT INTO public.credit_idempotency_keys
			(account_id, operation_type, idempotency_key, request_id, attempt_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING
	`, accountID, string(input.EntryType), input.IdempotencyKey, nullableString(input.RequestID), input.AttemptID)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: insert idempotency key: %w", err)
	}

	if result.RowsAffected() == 0 {
		existing, err := r.lookupExistingEntry(ctx, tx, accountID, input.EntryType, input.IdempotencyKey)
		if err != nil {
			return LedgerEntry{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return LedgerEntry{}, fmt.Errorf("ledger: commit duplicate tx: %w", err)
		}
		return existing, nil
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO public.credit_ledger_entries
			(account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		RETURNING id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at
	`, accountID, string(input.EntryType), input.CreditsDelta, input.IdempotencyKey, nullableString(input.RequestID), input.AttemptID, input.ReservationID, metadataBytes)

	entry, err := scanLedgerEntry(row)
	if err != nil {
		return LedgerEntry{}, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE public.credit_idempotency_keys
		SET ledger_entry_id = $4
		WHERE account_id = $1 AND operation_type = $2 AND idempotency_key = $3
	`, accountID, string(input.EntryType), input.IdempotencyKey, entry.ID)
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: update idempotency key: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: commit tx: %w", err)
	}

	return entry, nil
}

func (r *pgxRepository) GetBalance(ctx context.Context, accountID uuid.UUID) (BalanceSummary, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE
				WHEN entry_type IN ('grant', 'adjustment', 'usage_charge', 'refund') THEN credits_delta
				ELSE 0
			END), 0) AS posted_credits,
			ABS(COALESCE(SUM(CASE
				WHEN entry_type IN ('reservation_hold', 'reservation_release') THEN credits_delta
				ELSE 0
			END), 0)) AS reserved_credits
		FROM public.credit_ledger_entries
		WHERE account_id = $1
	`, accountID)

	var balance BalanceSummary
	if err := row.Scan(&balance.PostedCredits, &balance.ReservedCredits); err != nil {
		return BalanceSummary{}, fmt.Errorf("ledger: get balance: %w", err)
	}
	balance.AvailableCredits = balance.PostedCredits - balance.ReservedCredits

	return balance, nil
}

func (r *pgxRepository) ListEntries(ctx context.Context, accountID uuid.UUID, limit int) ([]LedgerEntry, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at
		FROM public.credit_ledger_entries
		WHERE account_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("ledger: list entries: %w", err)
	}
	defer rows.Close()

	var entries []LedgerEntry
	for rows.Next() {
		entry, err := scanLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: iterate entries: %w", err)
	}

	return entries, nil
}

func (r *pgxRepository) ListEntriesWithCursor(ctx context.Context, filter ListEntriesFilter) ([]LedgerEntry, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	var rows pgx.Rows
	var err error

	if filter.EntryType != nil && filter.Cursor != nil {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at
			FROM public.credit_ledger_entries
			WHERE account_id = $1 AND id < $2 AND entry_type = $3
			ORDER BY id DESC LIMIT $4
		`, filter.AccountID, filter.Cursor, string(*filter.EntryType), limit)
	} else if filter.EntryType != nil {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at
			FROM public.credit_ledger_entries
			WHERE account_id = $1 AND entry_type = $2
			ORDER BY id DESC LIMIT $3
		`, filter.AccountID, string(*filter.EntryType), limit)
	} else if filter.Cursor != nil {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at
			FROM public.credit_ledger_entries
			WHERE account_id = $1 AND id < $2
			ORDER BY id DESC LIMIT $3
		`, filter.AccountID, filter.Cursor, limit)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at
			FROM public.credit_ledger_entries
			WHERE account_id = $1
			ORDER BY id DESC LIMIT $2
		`, filter.AccountID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("ledger: list entries with cursor: %w", err)
	}
	defer rows.Close()

	var entries []LedgerEntry
	for rows.Next() {
		entry, err := scanLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: iterate entries: %w", err)
	}
	return entries, nil
}

func (r *pgxRepository) ListInvoices(ctx context.Context, accountID uuid.UUID) ([]InvoiceRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, payment_intent_id, invoice_number, status, credits, amount_usd, amount_local, local_currency, tax_treatment, rail, line_items, created_at
		FROM public.payment_invoices
		WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("ledger: list invoices: %w", err)
	}
	defer rows.Close()

	var invoices []InvoiceRow
	for rows.Next() {
		inv, err := scanInvoiceRow(rows)
		if err != nil {
			return nil, err
		}
		invoices = append(invoices, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: iterate invoices: %w", err)
	}
	return invoices, nil
}

func (r *pgxRepository) GetInvoice(ctx context.Context, accountID uuid.UUID, invoiceID uuid.UUID) (*InvoiceRow, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, payment_intent_id, invoice_number, status, credits, amount_usd, amount_local, local_currency, tax_treatment, rail, line_items, created_at
		FROM public.payment_invoices
		WHERE account_id = $1 AND id = $2
	`, accountID, invoiceID)

	inv, err := scanInvoiceRow(row)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func scanInvoiceRow(scanner entryScanner) (InvoiceRow, error) {
	var inv InvoiceRow
	var lineItemsBytes []byte
	if err := scanner.Scan(
		&inv.ID,
		&inv.AccountID,
		&inv.PaymentIntentID,
		&inv.InvoiceNumber,
		&inv.Status,
		&inv.Credits,
		&inv.AmountUSD,
		&inv.AmountLocal,
		&inv.LocalCurrency,
		&inv.TaxTreatment,
		&inv.Rail,
		&lineItemsBytes,
		&inv.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InvoiceRow{}, ErrNotFound
		}
		return InvoiceRow{}, fmt.Errorf("ledger: scan invoice: %w", err)
	}
	inv.LineItems = []map[string]any{}
	if len(lineItemsBytes) > 0 {
		if err := json.Unmarshal(lineItemsBytes, &inv.LineItems); err != nil {
			return InvoiceRow{}, fmt.Errorf("ledger: decode line items: %w", err)
		}
	}
	return inv, nil
}

func (r *pgxRepository) lookupExistingEntry(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, entryType EntryType, idempotencyKey string) (LedgerEntry, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at
		FROM public.credit_ledger_entries
		WHERE account_id = $1 AND entry_type = $2 AND idempotency_key = $3
	`, accountID, string(entryType), idempotencyKey)

	entry, err := scanLedgerEntry(row)
	if err != nil {
		return LedgerEntry{}, err
	}

	return entry, nil
}

type entryScanner interface {
	Scan(dest ...any) error
}

func scanLedgerEntry(scanner entryScanner) (LedgerEntry, error) {
	var entry LedgerEntry
	var requestID *string
	var metadataBytes []byte
	if err := scanner.Scan(
		&entry.ID,
		&entry.AccountID,
		&entry.EntryType,
		&entry.CreditsDelta,
		&entry.IdempotencyKey,
		&requestID,
		&entry.AttemptID,
		&entry.ReservationID,
		&metadataBytes,
		&entry.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LedgerEntry{}, ErrNotFound
		}
		return LedgerEntry{}, fmt.Errorf("ledger: scan entry: %w", err)
	}

	entry.Metadata = map[string]any{}
	if requestID != nil {
		entry.RequestID = *requestID
	}
	if len(metadataBytes) > 0 {
		if err := json.Unmarshal(metadataBytes, &entry.Metadata); err != nil {
			return LedgerEntry{}, fmt.Errorf("ledger: decode metadata: %w", err)
		}
	}

	return entry, nil
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func normalizeMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
