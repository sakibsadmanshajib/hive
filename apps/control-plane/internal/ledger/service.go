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
