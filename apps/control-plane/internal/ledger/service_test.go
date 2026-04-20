package ledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
)

type stubRepo struct {
	accountsMap   map[uuid.UUID]*accounts.Account
	memberships   []accounts.Membership
	invitations   map[string]*accounts.Invitation
	entries       map[uuid.UUID][]LedgerEntry
	lastListLimit int
	postErr       error
	balanceErr    error
	listErr       error
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		accountsMap: make(map[uuid.UUID]*accounts.Account),
		invitations: make(map[string]*accounts.Invitation),
		entries:     make(map[uuid.UUID][]LedgerEntry),
	}
}

func (s *stubRepo) PostEntry(_ context.Context, accountID uuid.UUID, input PostEntryInput) (LedgerEntry, error) {
	if s.postErr != nil {
		return LedgerEntry{}, s.postErr
	}

	for _, existing := range s.entries[accountID] {
		if existing.EntryType == input.EntryType && existing.IdempotencyKey == input.IdempotencyKey {
			return existing, nil
		}
	}

	entry := LedgerEntry{
		ID:             uuid.New(),
		AccountID:      accountID,
		EntryType:      input.EntryType,
		CreditsDelta:   input.CreditsDelta,
		IdempotencyKey: input.IdempotencyKey,
		RequestID:      input.RequestID,
		AttemptID:      input.AttemptID,
		ReservationID:  input.ReservationID,
		Metadata:       input.Metadata,
		CreatedAt:      time.Now().UTC(),
	}

	s.entries[accountID] = append(s.entries[accountID], entry)
	return entry, nil
}

func (s *stubRepo) GetBalance(_ context.Context, accountID uuid.UUID) (BalanceSummary, error) {
	if s.balanceErr != nil {
		return BalanceSummary{}, s.balanceErr
	}

	var posted int64
	var reservedNet int64
	for _, entry := range s.entries[accountID] {
		switch entry.EntryType {
		case EntryTypeGrant, EntryTypeAdjustment, EntryTypeUsageCharge, EntryTypeRefund:
			posted += entry.CreditsDelta
		case EntryTypeReservationHold, EntryTypeReservationRelease:
			reservedNet += entry.CreditsDelta
		}
	}

	reserved := reservedNet
	if reserved < 0 {
		reserved = -reserved
	}

	return BalanceSummary{
		PostedCredits:    posted,
		ReservedCredits:  reserved,
		AvailableCredits: posted - reserved,
	}, nil
}

func (s *stubRepo) ListEntries(_ context.Context, accountID uuid.UUID, limit int) ([]LedgerEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.lastListLimit = limit

	entries := s.entries[accountID]
	if limit <= 0 || len(entries) <= limit {
		return append([]LedgerEntry(nil), entries...), nil
	}

	return append([]LedgerEntry(nil), entries[:limit]...), nil
}

func (s *stubRepo) ListEntriesWithCursor(_ context.Context, filter ListEntriesFilter) ([]LedgerEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.lastListLimit = filter.Limit

	entries := s.entries[filter.AccountID]
	if filter.Limit <= 0 || len(entries) <= filter.Limit {
		return append([]LedgerEntry(nil), entries...), nil
	}

	return append([]LedgerEntry(nil), entries[:filter.Limit]...), nil
}

func (s *stubRepo) ListInvoices(_ context.Context, _ uuid.UUID) ([]InvoiceRow, error) {
	return nil, nil
}

func (s *stubRepo) GetInvoice(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*InvoiceRow, error) {
	return nil, ErrNotFound
}

func (s *stubRepo) ListMembershipsByUserID(_ context.Context, userID uuid.UUID) ([]accounts.Membership, error) {
	var memberships []accounts.Membership
	for _, membership := range s.memberships {
		if membership.UserID == userID {
			memberships = append(memberships, membership)
		}
	}

	return memberships, nil
}

func (s *stubRepo) CreateAccount(_ context.Context, acct accounts.Account) error {
	s.accountsMap[acct.ID] = &acct
	return nil
}

func (s *stubRepo) CreateMembership(_ context.Context, membership accounts.Membership) error {
	s.memberships = append(s.memberships, membership)
	return nil
}

func (s *stubRepo) CreateProfile(_ context.Context, _ accounts.AccountProfile) error {
	return nil
}

func (s *stubRepo) GetAccountByID(_ context.Context, id uuid.UUID) (*accounts.Account, error) {
	acct, ok := s.accountsMap[id]
	if !ok {
		return nil, accounts.ErrNotFound
	}

	return acct, nil
}

func (s *stubRepo) CreateInvitation(_ context.Context, invitation accounts.Invitation) error {
	s.invitations[invitation.TokenHash] = &invitation
	return nil
}

func (s *stubRepo) FindInvitationByTokenHash(_ context.Context, tokenHash string) (*accounts.Invitation, error) {
	invitation, ok := s.invitations[tokenHash]
	if !ok {
		return nil, accounts.ErrNotFound
	}

	return invitation, nil
}

func (s *stubRepo) AcceptInvitation(_ context.Context, invitationID uuid.UUID, acceptedAt time.Time) error {
	for _, invitation := range s.invitations {
		if invitation.ID == invitationID {
			invitation.AcceptedAt = &acceptedAt
			return nil
		}
	}

	return accounts.ErrNotFound
}

func (s *stubRepo) ListMembersByAccountID(_ context.Context, accountID uuid.UUID) ([]accounts.Member, error) {
	var members []accounts.Member
	for _, membership := range s.memberships {
		if membership.AccountID == accountID {
			members = append(members, accounts.Member{
				UserID: membership.UserID,
				Role:   membership.Role,
				Status: membership.Status,
			})
		}
	}

	return members, nil
}

func TestBalanceCalculation(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	accountID := uuid.New()

	if _, err := svc.GrantCredits(context.Background(), accountID, "grant-1", 100, map[string]any{"source": "test"}); err != nil {
		t.Fatalf("GrantCredits returned error: %v", err)
	}
	if _, err := svc.AdjustCredits(context.Background(), accountID, "adjust-1", -15, map[string]any{"reason": "correction"}); err != nil {
		t.Fatalf("AdjustCredits returned error: %v", err)
	}
	if _, err := repo.PostEntry(context.Background(), accountID, PostEntryInput{
		EntryType:      EntryTypeReservationHold,
		CreditsDelta:   -30,
		IdempotencyKey: "hold-1",
	}); err != nil {
		t.Fatalf("PostEntry reservation hold returned error: %v", err)
	}
	if _, err := repo.PostEntry(context.Background(), accountID, PostEntryInput{
		EntryType:      EntryTypeReservationRelease,
		CreditsDelta:   10,
		IdempotencyKey: "release-1",
	}); err != nil {
		t.Fatalf("PostEntry reservation release returned error: %v", err)
	}

	balance, err := svc.GetBalance(context.Background(), accountID)
	if err != nil {
		t.Fatalf("GetBalance returned error: %v", err)
	}

	if balance.PostedCredits != 85 {
		t.Fatalf("expected posted credits 85, got %d", balance.PostedCredits)
	}
	if balance.ReservedCredits != 20 {
		t.Fatalf("expected reserved credits 20, got %d", balance.ReservedCredits)
	}
	if balance.AvailableCredits != 65 {
		t.Fatalf("expected available credits 65, got %d", balance.AvailableCredits)
	}
}

func TestGrantCreditsRejectsBlankIdempotencyKey(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	_, err := svc.GrantCredits(context.Background(), uuid.New(), "   ", 100, nil)
	if err == nil {
		t.Fatal("expected GrantCredits to reject a blank idempotency key")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

func TestDuplicateGrantReturnsExistingEntry(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	accountID := uuid.New()

	first, err := svc.GrantCredits(context.Background(), accountID, "grant-duplicate", 250, map[string]any{"source": "purchase"})
	if err != nil {
		t.Fatalf("first GrantCredits returned error: %v", err)
	}

	second, err := svc.GrantCredits(context.Background(), accountID, "grant-duplicate", 250, map[string]any{"source": "purchase"})
	if err != nil {
		t.Fatalf("second GrantCredits returned error: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected duplicate grant to return entry %s, got %s", first.ID, second.ID)
	}

	if len(repo.entries[accountID]) != 1 {
		t.Fatalf("expected one stored ledger entry, got %d", len(repo.entries[accountID]))
	}
}
