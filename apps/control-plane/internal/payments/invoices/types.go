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
	ID                uuid.UUID
	WorkspaceID       uuid.UUID
	PeriodStart       time.Time
	PeriodEnd         time.Time
	TotalBDTSubunits  *big.Int
	LineItems         []InvoiceLineItem
	PDFStorageKey     string
	GeneratedAt       time.Time
}

// InvoiceLineItem is one row in the invoice line-items JSONB column.
//
// One bucket per (model). Aggregation happens in service.GenerateInvoiceForPeriod.
type InvoiceLineItem struct {
	ModelID      string   `json:"model_id"`
	RequestCount int64    `json:"request_count"`
	BDTSubunits  *big.Int `json:"bdt_subunits"`
}

// =============================================================================
// Sentinel errors
// =============================================================================

// ErrInvoiceNotFound is returned when an invoice does not exist or is not
// visible to the caller (cross-workspace requests are surfaced as 404 to avoid
// id-enumeration leakage).
var ErrInvoiceNotFound = errors.New("invoices: invoice not found")

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

	// AggregateByModel sums usage_charge ledger entries within [Start, End)
	// grouped by metadata->>'model'. Returns line items + total subunits.
	// All math via *big.Int.
	AggregateByModel(ctx context.Context, workspaceID uuid.UUID, period Period) ([]InvoiceLineItem, *big.Int, error)
}

// Storage is the narrow Supabase Storage surface required to write rendered
// PDFs and serve signed download URLs. Matches packages/storage.Storage but
// keeps this package's import surface minimal.
type Storage interface {
	Upload(ctx context.Context, bucket, key string, body interface{ Read(p []byte) (n int, err error) }, size int64, contentType string) error
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
