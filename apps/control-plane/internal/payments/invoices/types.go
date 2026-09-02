// Package invoices implements Phase 14 monthly BDT-only billing invoices.
//
// Distinct from `payments.Invoice` (the existing checkout-context struct that
// Phase 17 owns). Phase 14 ships a NEW invoice surface backed by the NEW
// public.invoices table (migration 20260428_01) — one row per (workspace,
// period_start) — rendered as a BDT-only PDF with zero USD/FX strings on the
// customer surface (regulatory requirement).
//
// Money policy: math/big for every BDT subunit aggregation. The BIGINT column
// stores the exact subunit count (paisa, 1 BDT = 100 paisa); application
// marshals via *big.Int.Int64() at the boundary. float64/float32 are banned
// in this package — verified by grep in PLAN verify block.
package invoices

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Period describes a closed-open monthly billing window:
// [Start, End) — inclusive at start, exclusive at end. UTC.
type Period struct {
	Start time.Time
	End   time.Time
}

// Invoice is one Phase 14 monthly invoice row.
//
// Currency is implicit (BDT) — DB CHECK enforces; the customer-facing wire
// format omits the field per regulatory rule (no FX/currency exchange language
// to BD customers). The PDF body uses "BDT" symbol explicitly.
type Invoice struct {
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	PeriodStart      time.Time
	PeriodEnd        time.Time
	TotalBDTSubunits *big.Int
	LineItems        []InvoiceLineItem
	PDFStorageKey    string
	GeneratedAt      time.Time

	// TotalCredits is the Hive credit quantity this invoice covers, the unit
	// the ledger actually stores (issue #1681). TotalBDTSubunits is what the
	// customer is charged for that quantity, converted once at USDBDTRate.
	//
	// nil means the quantity is unknown for this row rather than zero, which is
	// the honest state of an invoice generated between the issue #1648 fix and
	// issue #1682's repair: its taka is correct and its credit count was never
	// written down. Surfaces render an absent quantity as absent. Recovering it
	// by inverting the rate would round, and would present a manufactured
	// number as a ledger reading.
	TotalCredits *big.Int

	// USDBDTRate is the USD to BDT rate the subunit amounts on this row were
	// converted at, as a plain decimal string ("123.13"), or empty for a row
	// generated before issue #1648 was fixed. Recorded so the arithmetic on a
	// stored invoice stays reproducible from the ledger months later, and so
	// an operator can tell a converted row from a conflated legacy one. It is
	// server-side audit data and is deliberately absent from the customer
	// wire format and the PDF (D-035 left those FX tripwires in place).
	USDBDTRate string

	// USDBDTRateSource says which arm produced USDBDTRate: the account's own
	// FX snapshot, the operator environment override, or the platform default
	// (payments.RateSourceSnapshot, RateSourceEnv, RateSourceDefault). Recorded
	// alongside the rate so an operator reading a repaired row can tell an
	// account-specific denomination from a platform fallback without re-running
	// the resolution. Empty for a row that predates the conversion, and, like
	// the rate, absent from the customer wire format and the PDF.
	USDBDTRateSource string
}

// ModelCredits is one model's raw ledger aggregate for a period, in CREDITS.
//
// This type exists so the unit is visible at the seam. The repository reads
// credits, because credits are what `credit_ledger_entries.credits_delta`
// stores; only the service converts them into BDT subunits, at a rate it
// records. Issue #1648 was a credit count travelling under the name
// `bdt_subunits` all the way to the customer's screen, and a struct field
// named Credits is what stops that from being expressible again.
type ModelCredits struct {
	ModelID      string
	RequestCount int64
	Credits      *big.Int
}

// InvoiceLineItem is one row in the invoice line-items JSONB column.
//
// One bucket per (model). Aggregation happens in service.GenerateInvoiceForPeriod.
type InvoiceLineItem struct {
	ModelID      string   `json:"model_id"`
	RequestCount int64    `json:"request_count"`
	BDTSubunits  *big.Int `json:"bdt_subunits"`

	// Credits is the credit quantity this line covers, and BDTSubunits is what
	// it is charged at. Same nil-means-unknown rule as Invoice.TotalCredits.
	Credits *big.Int `json:"credits"`
}

// =============================================================================
// Sentinel errors
// =============================================================================

// ErrInvoiceNotFound is returned when an invoice does not exist or is not
// visible to the caller (cross-workspace requests are surfaced as 404 to avoid
// id-enumeration leakage).
var ErrInvoiceNotFound = errors.New("invoices: invoice not found")

// ErrAccessCheckUnavailable is returned when the workspace-membership check
// itself failed (DB error, nil checker). Surfaces as 500 — distinct from
// ErrInvoiceNotFound so the HTTP layer does not mask infra failures as 404.
var ErrAccessCheckUnavailable = errors.New("invoices: access check unavailable")

// =============================================================================
// Repository surface (defined where used per Go interface-placement convention)
// =============================================================================

// Repository is the data-access surface for invoices.
type Repository interface {
	// InsertOrFetch atomically inserts a new invoice OR returns the existing
	// row that matches (workspace_id, period_start). Idempotent — the cron may
	// be re-run safely.
	InsertOrFetch(ctx context.Context, in Invoice) (*Invoice, error)

	// GetByID fetches one invoice by primary key.
	GetByID(ctx context.Context, id uuid.UUID) (*Invoice, error)

	// ListByWorkspace fetches the most recent N invoices for a workspace.
	// Cursor pagination via period_start DESC; nil cursor = newest first.
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, limit int) ([]Invoice, error)

	// ListActiveWorkspaces returns workspace ids with at least one ledger
	// usage_charge entry within the supplied period — these are the only
	// candidates the monthly cron generates an invoice for.
	ListActiveWorkspaces(ctx context.Context, period Period) ([]uuid.UUID, error)

	// LatestUSDBDTRate returns the effective USD to BDT rate from the most
	// recent public.fx_snapshots row for the workspace taken STRICTLY BEFORE
	// `before`, as the decimal string stored there, or "" when the account has
	// no such snapshot.
	//
	// This is the rate the account was last known to have bought credits at:
	// checkout prices through payments.FXService and records the snapshot, so an
	// invoice for consuming those credits is denominated at a rate the customer
	// has actually seen. Callers pass the period end, so a top-up after the
	// month closed cannot re-denominate it and a regeneration months later
	// produces the same number the original run did. Credits are fungible and
	// prepaid, so a month funded by several top-ups at several rates is
	// denominated at the last one before it closed rather than at a blend.
	//
	// An account with no qualifying snapshot falls back to the platform rate.
	LatestUSDBDTRate(ctx context.Context, workspaceID uuid.UUID, before time.Time) (string, error)

	// ListUnconverted returns invoice rows written before the issue #1648 fix,
	// that is, rows whose usd_bdt_rate IS NULL. Their taka columns hold a raw
	// credit count rather than paisa. `limit` bounds one pass; zero or less
	// means no bound.
	//
	// The predicate, not a marker column, is what makes the repair idempotent:
	// a row stops matching the moment its rate is written, so a second pass
	// finds nothing to do.
	ListUnconverted(ctx context.Context, limit int) ([]Invoice, error)

	// UpdateConverted writes the repaired amounts, credit quantity, rate and
	// rate source onto an existing row, and reports whether a row was actually
	// written. The UPDATE carries the same `usd_bdt_rate IS NULL` guard, so a
	// row another process repaired first is left alone rather than converted
	// twice, and it reports false rather than an error.
	UpdateConverted(ctx context.Context, in Invoice) (bool, error)

	// AggregateByModel sums usage_charge ledger entries within [Start, End)
	// grouped by metadata->>'model'. Returns per-model CREDIT totals; the
	// conversion into BDT subunits belongs to the service, which owns the
	// rate. All math via *big.Int.
	AggregateByModel(ctx context.Context, workspaceID uuid.UUID, period Period) ([]ModelCredits, error)
}

// Storage is the narrow Supabase Storage surface required to write rendered
// PDFs and serve signed download URLs. Matches packages/storage.Storage but
// keeps this package's import surface minimal.
type Storage interface {
	Upload(ctx context.Context, bucket, key string, body interface {
		Read(p []byte) (n int, err error)
	}, size int64, contentType string) error
	PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// PDFRenderer is the PDF rendering surface. The production implementation is
// in pdf.go; tests stub via this interface.
type PDFRenderer interface {
	Render(inv Invoice, workspaceName string) ([]byte, error)
}

// AccessChecker reports whether userID may view a workspace's invoices.
//
// Phase 14 = workspace owner OR member-read; Phase 18 RBAC will swap the body.
type AccessChecker interface {
	IsWorkspaceMember(ctx context.Context, userID, workspaceID uuid.UUID) (bool, error)
}
