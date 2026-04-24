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
	billing      map[uuid.UUID]BillingProfile
	memberships  []accounts.Membership
	invitations  map[string]*accounts.Invitation
	acceptCalled bool
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		accountsMap: make(map[uuid.UUID]*accounts.Account),
		profiles:    make(map[uuid.UUID]AccountProfile),
		billing:     make(map[uuid.UUID]BillingProfile),
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

func (s *stubRepo) GetBillingProfile(_ context.Context, accountID uuid.UUID) (BillingProfile, error) {
	_, ok := s.accountsMap[accountID]
	if !ok {
		return BillingProfile{}, ErrNotFound
	}

	profile, ok := s.billing[accountID]
	if !ok {
		return BillingProfile{}, ErrNotFound
	}

	return profile, nil
}

func (s *stubRepo) UpsertBillingProfile(_ context.Context, accountID uuid.UUID, input UpdateBillingProfileInput) error {
	_, ok := s.accountsMap[accountID]
	if !ok {
		return ErrNotFound
	}

	s.billing[accountID] = BillingProfile{
		BillingContactName:       input.BillingContactName,
		BillingContactEmail:      input.BillingContactEmail,
		LegalEntityName:          input.LegalEntityName,
		LegalEntityType:          input.LegalEntityType,
		BusinessRegistrationNumber: input.BusinessRegistrationNumber,
		VATNumber:                input.VATNumber,
		TaxIDType:                input.TaxIDType,
		TaxIDValue:               input.TaxIDValue,
		CountryCode:              input.CountryCode,
		StateRegion:              input.StateRegion,
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

func TestUpdateBillingProfilePersistsBillingIdentity(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "acme-labs",
		DisplayName: "Acme Labs",
		AccountType: "business",
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice Smith",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Acme Labs",
		AccountType:          "business",
		CountryCode:          "US",
		StateRegion:          "CA",
		ProfileSetupComplete: true,
	}

	svc := NewService(repo)
	input := UpdateBillingProfileInput{
		BillingContactName:         "Alice Finance",
		BillingContactEmail:        "billing@acme.dev",
		LegalEntityName:            "Acme Labs LLC",
		LegalEntityType:            "private_company",
		BusinessRegistrationNumber: "BRN-12345",
		VATNumber:                  "US-999999",
		TaxIDType:                  "ein",
		TaxIDValue:                 "12-3456789",
		CountryCode:                "US",
		StateRegion:                "CA",
	}

	updated, err := svc.UpdateBillingProfile(context.Background(), accountID, input)
	if err != nil {
		t.Fatalf("UpdateBillingProfile error: %v", err)
	}

	if updated.BillingContactEmail != input.BillingContactEmail {
		t.Fatalf("expected billing_contact_email %q, got %q", input.BillingContactEmail, updated.BillingContactEmail)
	}
	if updated.LegalEntityType != input.LegalEntityType {
		t.Fatalf("expected legal_entity_type %q, got %q", input.LegalEntityType, updated.LegalEntityType)
	}
	if updated.VATNumber != input.VATNumber {
		t.Fatalf("expected vat_number %q, got %q", input.VATNumber, updated.VATNumber)
	}

	stored := repo.billing[accountID]
	if stored.LegalEntityName != input.LegalEntityName {
		t.Fatalf("expected stored legal_entity_name %q, got %q", input.LegalEntityName, stored.LegalEntityName)
	}
	if stored.TaxIDValue != input.TaxIDValue {
		t.Fatalf("expected stored tax_id_value %q, got %q", input.TaxIDValue, stored.TaxIDValue)
	}
}

func TestPartialBusinessBillingProfile(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "acme-labs",
		DisplayName: "Acme Labs",
		AccountType: "business",
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice Smith",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Acme Labs",
		AccountType:          "business",
		CountryCode:          "US",
		StateRegion:          "CA",
		ProfileSetupComplete: true,
	}

	svc := NewService(repo)
	updated, err := svc.UpdateBillingProfile(context.Background(), accountID, UpdateBillingProfileInput{
		LegalEntityName: "Acme Labs LLC",
		LegalEntityType: "private_company",
	})
	if err != nil {
		t.Fatalf("UpdateBillingProfile error: %v", err)
	}

	if updated.LegalEntityName != "Acme Labs LLC" {
		t.Fatalf("expected legal_entity_name %q, got %q", "Acme Labs LLC", updated.LegalEntityName)
	}
	if updated.BillingContactName != "" {
		t.Fatalf("expected billing_contact_name to remain empty, got %q", updated.BillingContactName)
	}
	if updated.VATNumber != "" {
		t.Fatalf("expected vat_number to remain empty, got %q", updated.VATNumber)
	}
}

func TestPersonalBillingProfileDefaultsLegalEntityType(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "alice-workspace",
		DisplayName: "Alice Workspace",
		AccountType: "personal",
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice Smith",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Alice Workspace",
		AccountType:          "personal",
		CountryCode:          "CA",
		StateRegion:          "ON",
		ProfileSetupComplete: true,
	}

	svc := NewService(repo)
	updated, err := svc.UpdateBillingProfile(context.Background(), accountID, UpdateBillingProfileInput{
		BillingContactName:  "Alice Smith",
		BillingContactEmail: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("UpdateBillingProfile error: %v", err)
	}

	if updated.LegalEntityType != "individual" {
		t.Fatalf("expected legal_entity_type %q, got %q", "individual", updated.LegalEntityType)
	}
	if repo.billing[accountID].LegalEntityType != "individual" {
		t.Fatalf("expected stored legal_entity_type %q, got %q", "individual", repo.billing[accountID].LegalEntityType)
	}
}
