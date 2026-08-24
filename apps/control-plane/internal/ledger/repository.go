package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	entry, err := PostEntryTx(ctx, tx, accountID, input)
	if err != nil {
		return LedgerEntry{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: commit tx: %w", err)
	}

	return entry, nil
}

// PostEntryTx writes one ledger entry inside a transaction the CALLER owns and
// commits. It exists so a write that must be atomic with a ledger entry can be
// exactly that, rather than two transactions and a hope.
//
// The caller today is the accounting repository's reservation create (issue
// #918): a reservation row and its hold used to be written separately, so a
// hold that failed to post left a row claiming credits the ledger never held,
// and the reaper later released that row against nothing. One transaction makes
// the invariant structural: the row exists if and only if its hold does.
//
// Same body as PostEntry, deliberately not duplicated: two implementations of a
// ledger write is how the two books drift apart.
func PostEntryTx(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, input PostEntryInput) (LedgerEntry, error) {
	metadataBytes, err := json.Marshal(stampCreditUnit(input.Metadata))
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: marshal metadata: %w", err)
	}

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
		// Already posted under this key: hand back the stored entry. The caller
		// commits, so a duplicate is still a clean no-op for whatever else its
		// transaction is doing.
		return lookupExistingEntry(ctx, tx, accountID, input.EntryType, input.IdempotencyKey)
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

	return entry, nil
}

// GetBalance sums the account's ledger into posted, reserved and available.
//
// Reserved is computed PER RESERVATION and clamped at zero, then added up. It
// used to be ABS of one account-wide sum, and that ABS is how issue #918 stayed
// invisible: a reservation released without ever having been held pushes the
// sum positive, and ABS renders that positive number as though credits were on
// hold. Worse, the phantom nets against genuine in-flight holds first, so the
// customer-facing available balance was wrong in BOTH directions on the two
// production accounts in that state, understating one by 2000 and overstating
// the other by 1000.
//
// A positive net for a single reservation is not a balance to render, it is a
// corruption signal: more credits came back than ever went out. It is reported
// separately in OverReleasedCredits, and Service.GetBalance logs it, rather
// than being folded into a number a customer would read as a hold.
//
// Cost, measured on a scratch database with the real schema, one account
// holding 5000 entries across 1667 reservations among 55000 rows: 6.2ms against
// the old query's 3.4ms, two index-driven scans and a hash aggregate instead of
// one streaming pass. That is the price of not lying about the number, paid
// inside the per-account critical section on the create path.
//
// It is deliberately NOT written as a CTE referenced twice: Postgres inlines a
// CTE only when it is referenced exactly once, so the obvious shape
// materializes the account's whole ledger slice into a tuplestore on every
// call. There is no Materialize or CTE Scan node in the plan above.
//
// ponytail: the hash aggregate is sized by the account's RESERVATION count
// (465kB at 1667 of them), so an account with a hundred thousand reservations
// spills past a default work_mem. Nothing near that exists today, and the
// upgrade path when it does is a rolled-up per-account balance rather than a
// smarter query.
func (r *pgxRepository) GetBalance(ctx context.Context, accountID uuid.UUID) (BalanceSummary, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE((
				SELECT SUM(credits_delta)
				FROM public.credit_ledger_entries
				WHERE account_id = $1
				  AND entry_type IN ('grant', 'adjustment', 'usage_charge', 'refund')
			), 0) AS posted_credits,
			COALESCE(SUM(GREATEST(-holds.net, 0)), 0) AS reserved_credits,
			COALESCE(SUM(GREATEST(holds.net, 0)), 0) AS over_released_credits,
			COALESCE(COUNT(*) FILTER (WHERE holds.net > 0), 0) AS over_released_reservations
		FROM (
			-- Grouped per reservation, and a NULL reservation_id groups per ROW.
			-- SQL puts every NULL in one bucket, which would net unrelated
			-- entries against each other and reintroduce, for exactly those
			-- rows, the account-wide netting this query exists to remove. No
			-- hold or release carries a NULL today (only grants do, and they are
			-- not in this set), but PostEntryInput.ReservationID is a pointer,
			-- so nothing stops one, and it would hide inside the shared bucket.
			SELECT SUM(credits_delta) AS net
			FROM public.credit_ledger_entries
			WHERE account_id = $1
			  AND entry_type IN ('reservation_hold', 'reservation_release')
			GROUP BY reservation_id, CASE WHEN reservation_id IS NULL THEN id END
		) AS holds
	`, accountID)

	var balance BalanceSummary
	if err := row.Scan(
		&balance.PostedCredits,
		&balance.ReservedCredits,
		&balance.OverReleasedCredits,
		&balance.OverReleasedReservations,
	); err != nil {
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

	// One parameterized query built from the optional filters replaces the
	// former per-combination branch tree (request_id doubled the cases).
	// Placeholder numbers derive from the running arg count; no value is
	// interpolated into the SQL text.
	const selectCols = `SELECT id, account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata, created_at FROM public.credit_ledger_entries`
	query := selectCols + ` WHERE account_id = $1`
	args := []any{filter.AccountID}
	next := func() int { return len(args) + 1 }

	if filter.Cursor != nil {
		query += fmt.Sprintf(` AND id < $%d`, next())
		args = append(args, *filter.Cursor)
	}
	if filter.EntryType != nil {
		query += fmt.Sprintf(` AND entry_type = $%d`, next())
		args = append(args, string(*filter.EntryType))
	}
	if v := strings.TrimSpace(filter.RequestID); v != "" {
		query += fmt.Sprintf(` AND request_id = $%d`, next())
		args = append(args, v)
	}

	query += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, next())
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, query, args...)
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

func lookupExistingEntry(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, entryType EntryType, idempotencyKey string) (LedgerEntry, error) {
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

// CreditUnitV2 stamps every ledger entry the CURRENT binary writes with the
// credit unit it speaks: 1 USD = 1e9 credits, effective with migration
// 20260823_40_credit_unit_rescale_billion.sql. Rows carrying the LEGACY key
// value ("legacy-1usd-100k-credits") were rescaled by that migration; rows
// stamped v2 are native new-unit; a nonzero entry carrying NEITHER was
// written by a pre-stamp binary after the rescale and is an unscaled
// straggler (the post-deploy detector in the migration header queries
// exactly that). Callers that pass their own credit_unit value win.
const CreditUnitV2 = "v2-1usd-1e9"

// stampCreditUnit returns a copy of metadata carrying CreditUnitV2 unless the
// caller already named a unit. It never mutates the caller's map.
func stampCreditUnit(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{"credit_unit": CreditUnitV2}
	}
	if _, ok := metadata["credit_unit"]; ok {
		return metadata
	}
	// No capacity hint: len+1 in a make() is the shape CodeQL's
	// allocation-overflow rule flags, and the hint buys nothing at this size.
	stamped := make(map[string]any)
	for k, v := range metadata {
		stamped[k] = v
	}
	stamped["credit_unit"] = CreditUnitV2
	return stamped
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
