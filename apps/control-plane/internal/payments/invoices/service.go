package invoices

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Phase 14 — Invoice service.
//
// Generates one BDT-only invoice per (workspace, period) by:
//   1. Aggregating ledger usage_charge entries grouped by model.
//   2. Rendering a BDT-only PDF (zero USD/FX strings — regulatory).
//   3. Uploading the PDF to Supabase Storage `hive-files`.
//   4. Inserting the invoice row (idempotent via UNIQUE(workspace_id,
//      period_start)).
//
// All math via *big.Int. No float64 in this file.
// =============================================================================

// FilesBucket is the Supabase Storage bucket where rendered PDFs land.
// (Matches deploy/docker storage configuration; pre-created bucket.)
const FilesBucket = "hive-files"

// PresignedURLTTL is the lifetime of a generated PDF download URL. Short TTL
// keeps the surface ephemeral; the console refreshes per-click.
const PresignedURLTTL = 10 * time.Minute

// WorkspaceNamer resolves a workspace UUID to a human-readable label for the
// PDF header. Defined where used (Go interface convention); production wires
// accounts.Service.
type WorkspaceNamer interface {
	WorkspaceName(ctx context.Context, workspaceID uuid.UUID) (string, error)
}

// Service orchestrates invoice generation, retrieval, and PDF download.
type Service struct {
	repo     Repository
	storage  storageBackend
	pdf      PDFRenderer
	access   AccessChecker
	naming   WorkspaceNamer
	logger   *slog.Logger
	now      func() time.Time
}

// storageBackend mirrors packages/storage.Storage but only the methods we use.
// Defined here so the package import surface remains narrow.
type storageBackend interface {
	Upload(ctx context.Context, bucket, key string, body bytesReader, size int64, contentType string) error
	PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// bytesReader is the minimal io.Reader subset Upload needs. The package
// /home/sakib/hive/packages/storage uses io.Reader; storageAdapter below
// bridges the two.
type bytesReader interface {
	Read(p []byte) (n int, err error)
}

// NewService constructs the invoice service.
func NewService(
	repo Repository,
	storage storageBackend,
	pdf PDFRenderer,
	access AccessChecker,
	naming WorkspaceNamer,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:    repo,
		storage: storage,
		pdf:     pdf,
		access:  access,
		naming:  naming,
		logger:  logger,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// GenerateInvoiceForPeriod aggregates ledger spend, renders the PDF, writes it
// to Supabase Storage, and persists the invoice row. Idempotent: re-running
// for the same (workspace, period_start) returns the existing invoice.
func (s *Service) GenerateInvoiceForPeriod(ctx context.Context, workspaceID uuid.UUID, period Period) (*Invoice, error) {
	if period.End.Before(period.Start) || period.End.Equal(period.Start) {
		return nil, fmt.Errorf("invoices: invalid period (end <= start)")
	}

	items, total, err := s.repo.AggregateByModel(ctx, workspaceID, period)
	if err != nil {
		return nil, fmt.Errorf("invoices: aggregate: %w", err)
	}
	if total == nil {
		total = new(big.Int)
	}

	// Render PDF first (cheap; lets us fail fast before any storage write).
	workspaceName := workspaceID.String()
	if s.naming != nil {
		if name, err := s.naming.WorkspaceName(ctx, workspaceID); err == nil && name != "" {
			workspaceName = name
		}
	}

	candidate := Invoice{
		WorkspaceID:      workspaceID,
		PeriodStart:      period.Start,
		PeriodEnd:        period.End,
		TotalBDTSubunits: total,
		LineItems:        items,
		GeneratedAt:      s.now(),
	}

	pdfBytes, err := s.pdf.Render(candidate, workspaceName)
	if err != nil {
		return nil, fmt.Errorf("invoices: render pdf: %w", err)
	}

	pdfKey := storageKeyFor(workspaceID, period.Start)
	if s.storage != nil {
		if err := s.storage.Upload(ctx, FilesBucket, pdfKey,
			bytes.NewReader(pdfBytes), int64(len(pdfBytes)), "application/pdf",
		); err != nil {
			return nil, fmt.Errorf("invoices: upload pdf: %w", err)
		}
	}
	candidate.PDFStorageKey = pdfKey

	saved, err := s.repo.InsertOrFetch(ctx, candidate)
	if err != nil {
		return nil, fmt.Errorf("invoices: persist: %w", err)
	}
	return saved, nil
}

// Get returns one invoice by id, gated on workspace membership of the caller.
//
// Cross-workspace access is surfaced as ErrInvoiceNotFound (404) — never 403 —
// to avoid id-enumeration leakage.
func (s *Service) Get(ctx context.Context, userID, invoiceID uuid.UUID) (*Invoice, error) {
	inv, err := s.repo.GetByID(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	if err := s.requireMembership(ctx, userID, inv.WorkspaceID); err != nil {
		return nil, ErrInvoiceNotFound
	}
	return inv, nil
}

// ListForWorkspace returns paginated invoices for a workspace; gated on
// membership.
func (s *Service) ListForWorkspace(ctx context.Context, userID, workspaceID uuid.UUID, limit int) ([]Invoice, error) {
	if err := s.requireMembership(ctx, userID, workspaceID); err != nil {
		return nil, ErrInvoiceNotFound
	}
	return s.repo.ListByWorkspace(ctx, workspaceID, limit)
}

// PDFURL returns a short-lived presigned URL for the invoice PDF. Gated on
// workspace membership; cross-workspace = 404.
func (s *Service) PDFURL(ctx context.Context, userID, invoiceID uuid.UUID) (string, error) {
	inv, err := s.repo.GetByID(ctx, invoiceID)
	if err != nil {
		return "", err
	}
	if err := s.requireMembership(ctx, userID, inv.WorkspaceID); err != nil {
		return "", ErrInvoiceNotFound
	}
	if inv.PDFStorageKey == "" || s.storage == nil {
		return "", ErrInvoiceNotFound
	}
	url, err := s.storage.PresignedURL(ctx, FilesBucket, inv.PDFStorageKey, PresignedURLTTL)
	if err != nil {
		return "", fmt.Errorf("invoices: presign: %w", err)
	}
	return url, nil
}

// =============================================================================
// Helpers
// =============================================================================

func (s *Service) requireMembership(ctx context.Context, userID, workspaceID uuid.UUID) error {
	if s.access == nil {
		return fmt.Errorf("invoices: access checker unavailable")
	}
	ok, err := s.access.IsWorkspaceMember(ctx, userID, workspaceID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("invoices: not a member")
	}
	return nil
}

// storageKeyFor builds the Supabase Storage object key for an invoice PDF.
//
//	invoices/{workspace}/{YYYY-MM}.pdf
//
// Period is keyed on UTC year-month so the path is deterministic and idempotent
// across cron re-runs.
func storageKeyFor(workspaceID uuid.UUID, periodStart time.Time) string {
	return fmt.Sprintf("invoices/%s/%s.pdf",
		workspaceID.String(),
		periodStart.UTC().Format("2006-01"),
	)
}

// PreviousMonth returns the calendar month immediately before `now` (UTC).
//
// Cron runs at 02:00 UTC on day 1; passing now = first-of-month yields a
// period of [first-of-prev-month, first-of-this-month).
func PreviousMonth(now time.Time) Period {
	t := now.UTC()
	end := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, -1, 0)
	return Period{Start: start, End: end}
}
