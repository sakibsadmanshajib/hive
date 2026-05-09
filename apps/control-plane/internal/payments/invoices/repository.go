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

	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.invoices (
			id, workspace_id, period_start, period_end,
			total_bdt_subunits, line_items, pdf_storage_key
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, period_start) DO NOTHING
		RETURNING id, workspace_id, period_start, period_end,
		          total_bdt_subunits, line_items, pdf_storage_key, generated_at
	`, id, in.WorkspaceID, in.PeriodStart, in.PeriodEnd,
		in.TotalBDTSubunits.Int64(), itemsJSON, in.PDFStorageKey)

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
		       total_bdt_subunits, line_items, pdf_storage_key, generated_at
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
		       total_bdt_subunits, line_items, pdf_storage_key, generated_at
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
		       total_bdt_subunits, line_items, pdf_storage_key, generated_at
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

// AggregateByModel sums usage_charge ledger entries grouped by metadata->>'model'.
//
// credits_delta is stored as a NEGATIVE integer for charges; we negate to a
// positive BDT subunit count (matching budgets.MonthToDateSpendBDT semantics).
//
// Unknown / missing model metadata buckets under the literal "unknown" key —
// guards against legacy ledger rows without metadata.
func (r *pgxRepository) AggregateByModel(ctx context.Context, workspaceID uuid.UUID, period Period) ([]InvoiceLineItem, *big.Int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(metadata->>'model', 'unknown') AS model_id,
		       COUNT(*)::bigint                         AS request_count,
		       COALESCE(SUM(-credits_delta), 0)::bigint AS bdt_subunits
		FROM public.credit_ledger_entries
		WHERE account_id = $1
		  AND entry_type = 'usage_charge'
		  AND created_at >= $2
		  AND created_at <  $3
		GROUP BY model_id
		ORDER BY model_id ASC
	`, workspaceID, period.Start, period.End)
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: aggregate by model: %w", err)
	}
	defer rows.Close()

	var (
		items []InvoiceLineItem
		total = new(big.Int)
	)
	for rows.Next() {
		var (
			modelID  string
			reqCount int64
			subunits int64
		)
		if err := rows.Scan(&modelID, &reqCount, &subunits); err != nil {
			return nil, nil, fmt.Errorf("invoices: scan aggregate row: %w", err)
		}
		if subunits < 0 {
			subunits = 0
		}
		item := InvoiceLineItem{
			ModelID:      modelID,
			RequestCount: reqCount,
			BDTSubunits:  big.NewInt(subunits),
		}
		items = append(items, item)
		total = total.Add(total, item.BDTSubunits)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("invoices: rows err: %w", err)
	}
	return items, total, nil
}

// =============================================================================
// Helpers
// =============================================================================

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInvoice(row rowScanner) (Invoice, error) {
	var (
		inv             Invoice
		totalSubunits   int64
		lineItemsBytes  []byte
		pdfStorageKey   *string
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
	); err != nil {
		return Invoice{}, err
	}
	inv.TotalBDTSubunits = big.NewInt(totalSubunits)
	if pdfStorageKey != nil {
		inv.PDFStorageKey = *pdfStorageKey
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
