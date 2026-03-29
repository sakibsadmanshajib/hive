package profiles

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
)

type stubRepo struct {
	accountsMap  map[uuid.UUID]*accounts.Account
	profiles     map[uuid.UUID]AccountProfile
	memberships  []accounts.Membership
	invitations  map[string]*accounts.Invitation
	acceptCalled bool
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		accountsMap: make(map[uuid.UUID]*accounts.Account),
		profiles:    make(map[uuid.UUID]AccountProfile),
		invitations: make(map[string]*accounts.Invitation),
	}
}

func (s *stubRepo) GetAccountProfile(_ context.Context, accountID uuid.UUID) (AccountProfile, error) {
	_, ok := s.accountsMap[accountID]
	if !ok {
		return AccountProfile{}, ErrNotFound
	}

	updated, ok := s.profiles[accountID]
	if !ok {
		return AccountProfile{}, ErrNotFound
	}

	return updated, nil
}

func (s *stubRepo) UpdateAccountProfile(_ context.Context, accountID uuid.UUID, input UpdateAccountProfileInput, profileSetupComplete bool) error {
	acct, ok := s.accountsMap[accountID]
	if !ok {
		return ErrNotFound
	}

	if _, ok := s.profiles[accountID]; !ok {
		return ErrNotFound
	}

	acct.DisplayName = input.DisplayName
	acct.AccountType = input.AccountType
	s.profiles[accountID] = AccountProfile{
		OwnerName:            input.OwnerName,
		LoginEmail:           input.LoginEmail,
		DisplayName:          input.DisplayName,
		AccountType:          input.AccountType,
		CountryCode:          input.CountryCode,
		StateRegion:          input.StateRegion,
		ProfileSetupComplete: profileSetupComplete,
	}
	return nil
}

func (s *stubRepo) ListMembershipsByUserID(_ context.Context, userID uuid.UUID) ([]accounts.Membership, error) {
	var result []accounts.Membership
	for _, membership := range s.memberships {
		if membership.UserID == userID {
			result = append(result, membership)
		}
	}
	return result, nil
}

func (s *stubRepo) CreateAccount(_ context.Context, acct accounts.Account) error {
	s.accountsMap[acct.ID] = &acct
	return nil
}

func (s *stubRepo) CreateMembership(_ context.Context, membership accounts.Membership) error {
	s.memberships = append(s.memberships, membership)
	return nil
}

func (s *stubRepo) CreateProfile(_ context.Context, profile accounts.AccountProfile) error {
	s.profiles[profile.AccountID] = AccountProfile{
		OwnerName:            profile.OwnerName,
		LoginEmail:           profile.LoginEmail,
		ProfileSetupComplete: profile.ProfileSetupComplete,
	}
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
			s.acceptCalled = true
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

func TestUpdateAccountProfile(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "legacy-workspace",
		DisplayName: "Legacy Workspace",
		AccountType: "personal",
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Legacy Workspace",
		AccountType:          "personal",
		ProfileSetupComplete: false,
	}

	svc := NewService(repo)
	input := UpdateAccountProfileInput{
		OwnerName:   "Alice Smith",
		LoginEmail:  "alice@example.com",
		DisplayName: "Acme Labs",
		AccountType: "business",
		CountryCode: "US",
		StateRegion: "CA",
	}

	updated, err := svc.UpdateAccountProfile(context.Background(), accountID, input)
	if err != nil {
		t.Fatalf("UpdateAccountProfile error: %v", err)
	}

	if updated.OwnerName != input.OwnerName {
		t.Fatalf("expected owner_name %q, got %q", input.OwnerName, updated.OwnerName)
	}
	if updated.LoginEmail != input.LoginEmail {
		t.Fatalf("expected login_email %q, got %q", input.LoginEmail, updated.LoginEmail)
	}
	if updated.DisplayName != input.DisplayName {
		t.Fatalf("expected display_name %q, got %q", input.DisplayName, updated.DisplayName)
	}
	if updated.AccountType != input.AccountType {
		t.Fatalf("expected account_type %q, got %q", input.AccountType, updated.AccountType)
	}
	if updated.CountryCode != input.CountryCode {
		t.Fatalf("expected country_code %q, got %q", input.CountryCode, updated.CountryCode)
	}
	if updated.StateRegion != input.StateRegion {
		t.Fatalf("expected state_region %q, got %q", input.StateRegion, updated.StateRegion)
	}
	if !updated.ProfileSetupComplete {
		t.Fatal("expected profile_setup_complete to be true after all required fields are present")
	}

	stored := repo.profiles[accountID]
	if !stored.ProfileSetupComplete {
		t.Fatal("expected stored profile_setup_complete to be true")
	}
	if repo.accountsMap[accountID].DisplayName != input.DisplayName {
		t.Fatalf("expected accounts.display_name %q, got %q", input.DisplayName, repo.accountsMap[accountID].DisplayName)
	}
	if repo.accountsMap[accountID].AccountType != input.AccountType {
		t.Fatalf("expected accounts.account_type %q, got %q", input.AccountType, repo.accountsMap[accountID].AccountType)
	}
}

func TestGetAccountProfileReportsIncompleteUntilAllCoreFieldsPresent(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "partial-workspace",
		DisplayName: "Partial Workspace",
		AccountType: "personal",
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Partial Workspace",
		AccountType:          "personal",
		ProfileSetupComplete: false,
	}

	svc := NewService(repo)

	profile, err := svc.GetAccountProfile(context.Background(), accountID)
	if err != nil {
		t.Fatalf("GetAccountProfile error: %v", err)
	}
	if profile.ProfileSetupComplete {
		t.Fatal("expected profile_setup_complete to stay false before country and state are provided")
	}

	updated, err := svc.UpdateAccountProfile(context.Background(), accountID, UpdateAccountProfileInput{
		OwnerName:   "Alice Smith",
		LoginEmail:  "alice@example.com",
		DisplayName: "Partial Workspace",
		AccountType: "personal",
		CountryCode: "US",
		StateRegion: "NY",
	})
	if err != nil {
		t.Fatalf("UpdateAccountProfile error: %v", err)
	}
	if !updated.ProfileSetupComplete {
		t.Fatal("expected profile_setup_complete to flip to true after all required fields are present")
	}
}

func TestUpdateAccountProfileRejectsInvalidAccountType(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "legacy-workspace",
		DisplayName: "Legacy Workspace",
		AccountType: "personal",
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Legacy Workspace",
		AccountType:          "personal",
		ProfileSetupComplete: false,
	}

	svc := NewService(repo)

	_, err := svc.UpdateAccountProfile(context.Background(), accountID, UpdateAccountProfileInput{
		OwnerName:   "Alice Smith",
		LoginEmail:  "alice@example.com",
		DisplayName: "Acme Labs",
		AccountType: "team",
		CountryCode: "US",
		StateRegion: "CA",
	})
	if err == nil {
		t.Fatal("expected validation error for invalid account_type")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if validationErr.Field != "account_type" {
		t.Fatalf("expected account_type validation error, got %q", validationErr.Field)
	}
	if repo.accountsMap[accountID].AccountType != "personal" {
		t.Fatalf("expected account_type to remain unchanged, got %q", repo.accountsMap[accountID].AccountType)
	}
}
