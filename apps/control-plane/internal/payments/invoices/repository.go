package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxRepository is the production pgx-backed Repository implementation.
type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by a pgxpool.Pool.
//
// All money arithmetic crosses the boundary as BIGINT subunits; application
// layer wraps in *big.Int at every read/write to keep math/big invariants
// (test in service_test.go asserts behaviour at the int64 boundary).
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

// =============================================================================
// InsertOrFetch — idempotent invoice creation.
//
// The migration enforces UNIQUE (workspace_id, period_start). We perform an
// INSERT ... ON CONFLICT DO NOTHING followed by a SELECT — the round-trip is
// deliberate so callers can reason about whether a fresh row was created.
// =============================================================================
func (r *pgxRepository) InsertOrFetch(ctx context.Context, in Invoice) (*Invoice, error) {
	if in.TotalBDTSubunits == nil {
		return nil, fmt.Errorf("invoices: total subunits must be non-nil")
	}
	if !in.TotalBDTSubunits.IsInt64() {
		return nil, fmt.Errorf("invoices: total overflows int64")
	}

	itemsJSON, err := encodeLineItems(in.LineItems)
	if err != nil {
		return nil, err
	}

	id := in.ID
	if id == uuid.Nil {
		id = uuid.New()
	}

	var rate *string
	if in.USDBDTRate != "" {
		rate = &in.USDBDTRate
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.invoices (
			id, workspace_id, period_start, period_end,
			total_bdt_subunits, line_items, pdf_storage_key, usd_bdt_rate
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::numeric)
		ON CONFLICT (workspace_id, period_start) DO NOTHING
		RETURNING id, workspace_id, period_start, period_end,
		          total_bdt_subunits, line_items, pdf_storage_key, generated_at,
		          usd_bdt_rate::text
	`, id, in.WorkspaceID, in.PeriodStart, in.PeriodEnd,
		in.TotalBDTSubunits.Int64(), itemsJSON, in.PDFStorageKey, rate)

	got, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Conflict path — fetch the existing row.
			return r.fetchByWorkspacePeriod(ctx, in.WorkspaceID, in.PeriodStart)
		}
		return nil, fmt.Errorf("invoices: insert: %w", err)
	}
	return &got, nil
}

func (r *pgxRepository) fetchByWorkspacePeriod(ctx context.Context, workspaceID uuid.UUID, periodStart time.Time) (*Invoice, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, workspace_id, period_start, period_end,
		       total_bdt_subunits, line_items, pdf_storage_key, generated_at,
		       usd_bdt_rate::text
		FROM public.invoices
		WHERE workspace_id = $1 AND period_start = $2
	`, workspaceID, periodStart)
	got, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("invoices: fetch by workspace+period: %w", err)
	}
	return &got, nil
}

func (r *pgxRepository) GetByID(ctx context.Context, id uuid.UUID) (*Invoice, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, workspace_id, period_start, period_end,
		       total_bdt_subunits, line_items, pdf_storage_key, generated_at,
		       usd_bdt_rate::text
		FROM public.invoices
		WHERE id = $1
	`, id)
	got, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("invoices: get by id: %w", err)
	}
	return &got, nil
}

func (r *pgxRepository) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, limit int) ([]Invoice, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, workspace_id, period_start, period_end,
		       total_bdt_subunits, line_items, pdf_storage_key, generated_at,
		       usd_bdt_rate::text
		FROM public.invoices
		WHERE workspace_id = $1
		ORDER BY period_start DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("invoices: list by workspace: %w", err)
	}
	defer rows.Close()

	var out []Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoices: rows: %w", err)
	}
	return out, nil
}

// ListActiveWorkspaces returns the set of accounts that posted at least one
// usage_charge within [period.Start, period.End). The cron uses this set —
// not all workspaces — so workspaces with zero traffic don't generate empty
// invoice rows.
func (r *pgxRepository) ListActiveWorkspaces(ctx context.Context, period Period) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT account_id
		FROM public.credit_ledger_entries
		WHERE entry_type = 'usage_charge'
		  AND created_at >= $1
		  AND created_at <  $2
	`, period.Start, period.End)
	if err != nil {
		return nil, fmt.Errorf("invoices: list active workspaces: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("invoices: scan ws id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LatestUSDBDTRate reads the effective rate off the account's newest FX snapshot
// taken before `before`. public.fx_snapshots carries an index on
// (account_id, created_at desc), so this is an index scan with a filter on the
// two currency columns, which the index does not cover; both default to USD and
// BDT and CreateSnapshot hardcodes them, so it is one row in practice.
//
// The `created_at < before` bound is what makes a closed month immutable.
// Callers pass the invoice period end, so a top-up on the last day of the month
// cannot re-denominate credits consumed on the second, and regenerating a
// corrected invoice in October produces the figure the original run did rather
// than October's rate.
//
// No rows is not an error: an account that has never paid through a BDT rail (a
// granted or card-funded workspace) has no rate of its own, and the caller falls
// back to the platform rate.
func (r *pgxRepository) LatestUSDBDTRate(ctx context.Context, workspaceID uuid.UUID, before time.Time) (string, error) {
	var rate string
	err := r.pool.QueryRow(ctx, `
		SELECT effective_rate
		FROM public.fx_snapshots
		WHERE account_id = $1
		  AND base_currency = 'USD'
		  AND quote_currency = 'BDT'
		  AND created_at < $2
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, before).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("invoices: latest fx snapshot: %w", err)
	}
	return rate, nil
}

// AggregateByModel sums usage_charge ledger entries grouped by
// metadata->>'model' and returns the result in CREDITS, the unit the ledger
// actually stores.
//
// credits_delta is negative for a charge, so the sum is negated back to a
// positive quantity. It is NOT a BDT subunit count: converting credits into
// taka is an FX step and it belongs to the service, which records the rate it
// used. This function used to alias the two units and the console rendered a
// credit count as paisa (issue #1648).
//
// Unknown / missing model metadata buckets under the literal "unknown" key —
// guards against legacy ledger rows without metadata.
func (r *pgxRepository) AggregateByModel(ctx context.Context, workspaceID uuid.UUID, period Period) ([]ModelCredits, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(metadata->>'model', 'unknown') AS model_id,
		       COUNT(*)::bigint                         AS request_count,
		       COALESCE(SUM(-credits_delta), 0)::bigint AS credits
		FROM public.credit_ledger_entries
		WHERE account_id = $1
		  AND entry_type = 'usage_charge'
		  AND created_at >= $2
		  AND created_at <  $3
		GROUP BY model_id
		ORDER BY model_id ASC
	`, workspaceID, period.Start, period.End)
	if err != nil {
		return nil, fmt.Errorf("invoices: aggregate by model: %w", err)
	}
	defer rows.Close()

	var out []ModelCredits
	for rows.Next() {
		var (
			modelID  string
			reqCount int64
			credits  int64
		)
		if err := rows.Scan(&modelID, &reqCount, &credits); err != nil {
			return nil, fmt.Errorf("invoices: scan aggregate row: %w", err)
		}
		if credits < 0 {
			credits = 0
		}
		out = append(out, ModelCredits{
			ModelID:      modelID,
			RequestCount: reqCount,
			Credits:      big.NewInt(credits),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invoices: rows err: %w", err)
	}
	return out, nil
}

// =============================================================================
// Helpers
// =============================================================================

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInvoice(row rowScanner) (Invoice, error) {
	var (
		inv            Invoice
		totalSubunits  int64
		lineItemsBytes []byte
		pdfStorageKey  *string
		usdBDTRate     *string
	)
	if err := row.Scan(
		&inv.ID,
		&inv.WorkspaceID,
		&inv.PeriodStart,
		&inv.PeriodEnd,
		&totalSubunits,
		&lineItemsBytes,
		&pdfStorageKey,
		&inv.GeneratedAt,
		&usdBDTRate,
	); err != nil {
		return Invoice{}, err
	}
	inv.TotalBDTSubunits = big.NewInt(totalSubunits)
	if pdfStorageKey != nil {
		inv.PDFStorageKey = *pdfStorageKey
	}
	// NULL means a row generated before issue #1648: its amounts are a raw
	// credit count, not taka. Left empty rather than defaulted so the two are
	// distinguishable.
	if usdBDTRate != nil {
		inv.USDBDTRate = *usdBDTRate
	}
	items, err := decodeLineItems(lineItemsBytes)
	if err != nil {
		return Invoice{}, err
	}
	inv.LineItems = items
	return inv, nil
}

// encodeLineItems renders LineItems as JSONB. *big.Int subunits are encoded as
// strings to dodge JS Number precision concerns; the wire format uses int64
// (BDT subunits fit) but the JSONB column is read by analytics tools that may
// be string-tolerant.
type lineItemJSON struct {
	ModelID      string `json:"model_id"`
	RequestCount int64  `json:"request_count"`
	BDTSubunits  string `json:"bdt_subunits"`
}

func encodeLineItems(items []InvoiceLineItem) ([]byte, error) {
	out := make([]lineItemJSON, 0, len(items))
	for _, it := range items {
		amount := "0"
		if it.BDTSubunits != nil {
			amount = it.BDTSubunits.String()
		}
		out = append(out, lineItemJSON{
			ModelID:      it.ModelID,
			RequestCount: it.RequestCount,
			BDTSubunits:  amount,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("invoices: marshal line items: %w", err)
	}
	return b, nil
}

func decodeLineItems(raw []byte) ([]InvoiceLineItem, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var parsed []lineItemJSON
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("invoices: unmarshal line items: %w", err)
	}
	out := make([]InvoiceLineItem, 0, len(parsed))
	for _, p := range parsed {
		amount := new(big.Int)
		if p.BDTSubunits != "" {
			if _, ok := amount.SetString(p.BDTSubunits, 10); !ok {
				return nil, fmt.Errorf("invoices: invalid bdt_subunits %q", p.BDTSubunits)
			}
		}
		out = append(out, InvoiceLineItem{
			ModelID:      p.ModelID,
			RequestCount: p.RequestCount,
			BDTSubunits:  amount,
		})
	}
	return out, nil
}
