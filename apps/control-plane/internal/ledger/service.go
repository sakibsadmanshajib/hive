package ledger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository

	// account id -> last time its over-release anomaly was logged (issue #918).
	anomalyLogged sync.Map
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetBalance(ctx context.Context, accountID uuid.UUID) (BalanceSummary, error) {
	balance, err := s.repo.GetBalance(ctx, accountID)
	if err != nil {
		return balance, err
	}
	// A reservation cannot legitimately have more released than held, so this
	// is a corruption signal and the only place the system notices it (issue
	// #918). Logged at error, throttled per account below, because it is not
	// self-healing, it needs an operator, and it stayed hidden for a month
	// while ABS made it look like an ordinary hold. It does not fail the read,
	// because refusing to answer would take an affected account off the air
	// without repairing anything, and the balance being returned is now the
	// honest one.
	if balance.OverReleasedReservations > 0 && s.shouldLogAnomaly(accountID) {
		slog.ErrorContext(ctx, "ledger: account has reservations released beyond what was ever held, manual reconciliation required",
			"account_id", accountID.String(),
			"over_released_reservations", balance.OverReleasedReservations,
			"over_released_credits", balance.OverReleasedCredits)
	}
	return balance, nil
}

// shouldLogAnomaly rate-limits the line above to once per account per interval.
//
// The condition is persistent by construction: it needs an operator and cannot
// clear itself. GetBalance runs on every reservation create and on every
// balance read, so logging each time would emit at whatever rate the tenant
// sends traffic, which makes the error stream useless exactly for the account
// that needs attention.
//
// ponytail: in-process and per instance, so N instances emit N lines per
// interval. That is the right ceiling for a signal meant to be noticed rather
// than counted; if it ever needs to be counted, this is where a metric goes.
func (s *Service) shouldLogAnomaly(accountID uuid.UUID) bool {
	const interval = 15 * time.Minute
	now := time.Now()
	last, seen := s.anomalyLogged.Load(accountID)
	if seen && now.Sub(last.(time.Time)) < interval {
		return false
	}
	s.anomalyLogged.Store(accountID, now)
	return true
}

func (s *Service) ListEntries(ctx context.Context, accountID uuid.UUID, limit int) ([]LedgerEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.repo.ListEntries(ctx, accountID, limit)
}

func (s *Service) ListEntriesWithCursor(ctx context.Context, filter ListEntriesFilter) ([]LedgerEntry, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	return s.repo.ListEntriesWithCursor(ctx, filter)
}

func (s *Service) ListInvoices(ctx context.Context, accountID uuid.UUID) ([]InvoiceRow, error) {
	return s.repo.ListInvoices(ctx, accountID)
}

func (s *Service) GetInvoice(ctx context.Context, accountID uuid.UUID, invoiceID uuid.UUID) (*InvoiceRow, error) {
	return s.repo.GetInvoice(ctx, accountID, invoiceID)
}

func (s *Service) GrantCredits(ctx context.Context, accountID uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (LedgerEntry, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return LedgerEntry{}, &ValidationError{Field: "idempotency_key", Message: "idempotency_key is required"}
	}
	if credits <= 0 {
		return LedgerEntry{}, &ValidationError{Field: "credits", Message: "credits must be greater than zero"}
	}

	entry, err := s.repo.PostEntry(ctx, accountID, PostEntryInput{
		EntryType:      EntryTypeGrant,
		CreditsDelta:   credits,
		IdempotencyKey: idempotencyKey,
		Metadata:       normalizeMetadata(metadata),
	})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: grant credits: %w", err)
	}

	return entry, nil
}

func (s *Service) AdjustCredits(ctx context.Context, accountID uuid.UUID, idempotencyKey string, creditsDelta int64, metadata map[string]any) (LedgerEntry, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return LedgerEntry{}, &ValidationError{Field: "idempotency_key", Message: "idempotency_key is required"}
	}
	if creditsDelta == 0 {
		return LedgerEntry{}, &ValidationError{Field: "credits_delta", Message: "credits_delta must not be zero"}
	}

	entry, err := s.repo.PostEntry(ctx, accountID, PostEntryInput{
		EntryType:      EntryTypeAdjustment,
		CreditsDelta:   creditsDelta,
		IdempotencyKey: idempotencyKey,
		Metadata:       normalizeMetadata(metadata),
	})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: adjust credits: %w", err)
	}

	return entry, nil
}

func (s *Service) ReserveCredits(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (LedgerEntry, error) {
	return s.postUsageEntry(ctx, accountID, EntryTypeReservationHold, requestID, attemptID, reservationID, idempotencyKey, -credits, metadata)
}

func (s *Service) ReleaseReservedCredits(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (LedgerEntry, error) {
	return s.postUsageEntry(ctx, accountID, EntryTypeReservationRelease, requestID, attemptID, reservationID, idempotencyKey, credits, metadata)
}

func (s *Service) ChargeUsage(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (LedgerEntry, error) {
	return s.postUsageEntry(ctx, accountID, EntryTypeUsageCharge, requestID, attemptID, reservationID, idempotencyKey, -credits, metadata)
}

func (s *Service) RefundCredits(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (LedgerEntry, error) {
	return s.postUsageEntry(ctx, accountID, EntryTypeRefund, requestID, attemptID, reservationID, idempotencyKey, credits, metadata)
}

func (s *Service) postUsageEntry(ctx context.Context, accountID uuid.UUID, entryType EntryType, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, creditsDelta int64, metadata map[string]any) (LedgerEntry, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return LedgerEntry{}, &ValidationError{Field: "idempotency_key", Message: "idempotency_key is required"}
	}
	if creditsDelta == 0 {
		return LedgerEntry{}, &ValidationError{Field: "credits", Message: "credits must be greater than zero"}
	}

	entry, err := s.repo.PostEntry(ctx, accountID, PostEntryInput{
		EntryType:      entryType,
		CreditsDelta:   creditsDelta,
		IdempotencyKey: idempotencyKey,
		RequestID:      strings.TrimSpace(requestID),
		AttemptID:      attemptID,
		ReservationID:  reservationID,
		Metadata:       normalizeMetadata(metadata),
	})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: post %s: %w", entryType, err)
	}

	return entry, nil
}
