package ledger

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetBalance(ctx context.Context, accountID uuid.UUID) (BalanceSummary, error) {
	return s.repo.GetBalance(ctx, accountID)
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
