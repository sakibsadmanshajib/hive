package accounts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// --- stub repository ---

type stubRepo struct {
	memberships  []accounts.Membership
	accountsMap  map[uuid.UUID]*accounts.Account
	invitations  map[string]*accounts.Invitation // token -> invitation
	profiles     map[uuid.UUID]*accounts.AccountProfile
	acceptCalled bool
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		accountsMap: make(map[uuid.UUID]*accounts.Account),
		invitations: make(map[string]*accounts.Invitation),
		profiles:    make(map[uuid.UUID]*accounts.AccountProfile),
	}
}

func (s *stubRepo) ListMembershipsByUserID(_ context.Context, userID uuid.UUID) ([]accounts.Membership, error) {
	var result []accounts.Membership
	for _, m := range s.memberships {
		if m.UserID == userID {
			result = append(result, m)
		}
	}
	return result, nil
}

func (s *stubRepo) CreateAccount(_ context.Context, acct accounts.Account) error {
	s.accountsMap[acct.ID] = &acct
	return nil
}

func (s *stubRepo) CreateMembership(_ context.Context, m accounts.Membership) error {
	s.memberships = append(s.memberships, m)
	return nil
}

func (s *stubRepo) CreateProfile(_ context.Context, p accounts.AccountProfile) error {
	s.profiles[p.AccountID] = &p
	return nil
}

func (s *stubRepo) GetAccountByID(_ context.Context, id uuid.UUID) (*accounts.Account, error) {
	a, ok := s.accountsMap[id]
	if !ok {
		return nil, accounts.ErrNotFound
	}
	return a, nil
}

func (s *stubRepo) CreateInvitation(_ context.Context, inv accounts.Invitation) error {
	s.invitations[inv.TokenHash] = &inv
	return nil
}

func (s *stubRepo) FindInvitationByTokenHash(_ context.Context, tokenHash string) (*accounts.Invitation, error) {
	inv, ok := s.invitations[tokenHash]
	if !ok {
		return nil, accounts.ErrNotFound
	}
	return inv, nil
}

func (s *stubRepo) AcceptInvitation(_ context.Context, invitationID uuid.UUID, acceptedAt time.Time) error {
	for _, inv := range s.invitations {
		if inv.ID == invitationID {
			inv.AcceptedAt = &acceptedAt
			s.acceptCalled = true
			return nil
		}
	}
	return accounts.ErrNotFound
}

func (s *stubRepo) ListMembersByAccountID(_ context.Context, accountID uuid.UUID) ([]accounts.Member, error) {
	var members []accounts.Member
	for _, m := range s.memberships {
		if m.AccountID == accountID {
			members = append(members, accounts.Member{
				UserID: m.UserID,
				Role:   m.Role,
				Status: m.Status,
			})
		}
	}
	return members, nil
}

// --- TestEnsureDefaultAccount ---

func TestEnsureDefaultAccount_CreatesWorkspaceOnFirstLogin(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "alice@example.com",
		EmailVerified: true,
		FullName:      "Alice Smith",
	}

	vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	// One membership was provisioned.
	if len(vc.Memberships) != 1 {
		t.Fatalf("expected 1 membership, got %d", len(vc.Memberships))
	}
	if vc.Memberships[0].Role != "owner" {
		t.Errorf("expected owner role, got %s", vc.Memberships[0].Role)
	}

	// Default workspace name seeds from full name.
	if vc.CurrentAccount.DisplayName != "Alice Smith's Workspace" {
		t.Errorf("unexpected display name: %q", vc.CurrentAccount.DisplayName)
	}

	// Profile row exists.
	if _, ok := repo.profiles[vc.CurrentAccount.ID]; !ok {
		t.Error("account_profiles row not created")
	}
}

func TestEnsureDefaultAccount_FallbackDisplayNameFromEmail(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "bob@example.com",
		EmailVerified: false,
		FullName:      "",
	}

	vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	// Display name falls back to email local part.
	if vc.CurrentAccount.DisplayName != "bob's Workspace" {
		t.Errorf("unexpected display name: %q", vc.CurrentAccount.DisplayName)
	}
}

func TestEnsureDefaultAccount_UnverifiedGatesAreFalse(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "unverified@example.com",
		EmailVerified: false,
		FullName:      "Unverified User",
	}

	vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	if vc.Gates.CanInviteMembers {
		t.Error("expected can_invite_members=false for unverified user")
	}
	if vc.Gates.CanManageAPIKeys {
		t.Error("expected can_manage_api_keys=false for unverified user")
	}
}

func TestEnsureDefaultAccount_VerifiedOwnerGatesAreTrue(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "owner@example.com",
		EmailVerified: true,
		FullName:      "Owner User",
	}

	vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	if !vc.Gates.CanInviteMembers {
		t.Error("expected can_invite_members=true for verified owner")
	}
	if !vc.Gates.CanManageAPIKeys {
		t.Error("expected can_manage_api_keys=true for verified owner")
	}
}

// --- TestInvitationRequiresVerifiedEmail ---

func TestInvitationRequiresVerifiedEmail(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	ownerUserID := uuid.New()
	accountID := uuid.New()

	// Pre-populate account and owner membership.
	acct := accounts.Account{
		ID:          accountID,
		Slug:        "test-workspace",
		DisplayName: "Test Workspace",
		AccountType: "personal",
		OwnerUserID: ownerUserID,
	}
	repo.accountsMap[accountID] = &acct
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    ownerUserID,
		Role:      "owner",
		Status:    "active",
	})

	viewer := auth.Viewer{
		UserID:        ownerUserID,
		Email:         "owner@example.com",
		EmailVerified: false,
	}

	_, err := svc.CreateInvitation(context.Background(), accountID, viewer, "member@example.com")
	if err == nil {
		t.Fatal("expected error for unverified owner, got nil")
	}

	var gateErr *accounts.GateError
	if !accounts.AsGateError(err, &gateErr) {
		t.Fatalf("expected GateError, got %T: %v", err, err)
	}
	if gateErr.Code != "email_verification_required" {
		t.Errorf("expected code email_verification_required, got %q", gateErr.Code)
	}
}

func TestInvitationVerifiedOwnerSuccess(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	ownerUserID := uuid.New()
	accountID := uuid.New()

	acct := accounts.Account{
		ID:          accountID,
		Slug:        "test-workspace",
		DisplayName: "Test Workspace",
		AccountType: "personal",
		OwnerUserID: ownerUserID,
	}
	repo.accountsMap[accountID] = &acct
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    ownerUserID,
		Role:      "owner",
		Status:    "active",
	})

	viewer := auth.Viewer{
		UserID:        ownerUserID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}

	inv, err := svc.CreateInvitation(context.Background(), accountID, viewer, "member@example.com")
	if err != nil {
		t.Fatalf("CreateInvitation error: %v", err)
	}

	if inv.Token == "" {
		t.Error("expected non-empty invitation token")
	}
	if inv.Email != "member@example.com" {
		t.Errorf("unexpected invitation email: %q", inv.Email)
	}
}

// --- TestSelectCurrentAccount ---

func TestSelectCurrentAccount_ExplicitHeader(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	userID := uuid.New()
	acct1ID := uuid.New()
	acct2ID := uuid.New()

	for _, acct := range []accounts.Account{
		{ID: acct1ID, Slug: "workspace-1", DisplayName: "Workspace 1", AccountType: "personal", OwnerUserID: userID},
		{ID: acct2ID, Slug: "workspace-2", DisplayName: "Workspace 2", AccountType: "personal", OwnerUserID: userID},
	} {
		a := acct
		repo.accountsMap[a.ID] = &a
	}

	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: acct1ID, UserID: userID, Role: "owner", Status: "active"},
		{ID: uuid.New(), AccountID: acct2ID, UserID: userID, Role: "member", Status: "active"},
	}

	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "user@example.com",
		EmailVerified: true,
	}

	// Request workspace 2 explicitly.
	vc, err := svc.EnsureViewerContext(context.Background(), viewer, acct2ID)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	if vc.CurrentAccount.ID != acct2ID {
		t.Errorf("expected current account %v, got %v", acct2ID, vc.CurrentAccount.ID)
	}
}

func TestSelectCurrentAccount_FallbackOnInvalidHeader(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	userID := uuid.New()
	acct1ID := uuid.New()
	unknownID := uuid.New()

	acct1 := accounts.Account{ID: acct1ID, Slug: "my-workspace", DisplayName: "My Workspace", AccountType: "personal", OwnerUserID: userID}
	repo.accountsMap[acct1ID] = &acct1
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: acct1ID, UserID: userID, Role: "owner", Status: "active"},
	}

	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "user@example.com",
		EmailVerified: true,
	}

	// Request a workspace the user doesn't belong to — should fall back.
	vc, err := svc.EnsureViewerContext(context.Background(), viewer, unknownID)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	if vc.CurrentAccount.ID != acct1ID {
		t.Errorf("expected fallback to account %v, got %v", acct1ID, vc.CurrentAccount.ID)
	}
}

// --- TestAcceptInvitation ---

func TestAcceptInvitation_CreatesActiveMembership(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	ownerUserID := uuid.New()
	inviteeUserID := uuid.New()
	accountID := uuid.New()

	acct := accounts.Account{
		ID:          accountID,
		Slug:        "shared-workspace",
		DisplayName: "Shared Workspace",
		AccountType: "personal",
		OwnerUserID: ownerUserID,
	}
	repo.accountsMap[accountID] = &acct
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: ownerUserID, Role: "owner", Status: "active"},
	}

	// Directly insert an invitation with a known token hash.
	rawToken := "test-raw-token-abc123"
	tokenHash := accounts.HashToken(rawToken)
	expires := time.Now().Add(72 * time.Hour)
	invID := uuid.New()
	repo.invitations[tokenHash] = &accounts.Invitation{
		ID:              invID,
		AccountID:       accountID,
		Email:           "invitee@example.com",
		Role:            "member",
		TokenHash:       tokenHash,
		ExpiresAt:       expires,
		InvitedByUserID: ownerUserID,
	}

	viewer := auth.Viewer{
		UserID:        inviteeUserID,
		Email:         "invitee@example.com",
		EmailVerified: true,
	}

	joinedAccountID, err := svc.AcceptInvitation(context.Background(), viewer, rawToken)
	if err != nil {
		t.Fatalf("AcceptInvitation error: %v", err)
	}

	if joinedAccountID != accountID {
		t.Errorf("expected joined account %v, got %v", accountID, joinedAccountID)
	}

	// A new active membership should exist for the invitee.
	found := false
	for _, m := range repo.memberships {
		if m.UserID == inviteeUserID && m.AccountID == accountID && m.Status == "active" && m.Role == "member" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected active member membership for invitee")
	}
}

func TestAcceptInvitation_EmailMismatchFails(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	ownerUserID := uuid.New()
	accountID := uuid.New()

	acct := accounts.Account{
		ID:          accountID,
		Slug:        "workspace",
		DisplayName: "Workspace",
		AccountType: "personal",
		OwnerUserID: ownerUserID,
	}
	repo.accountsMap[accountID] = &acct

	rawToken := "mismatch-token"
	tokenHash := accounts.HashToken(rawToken)
	repo.invitations[tokenHash] = &accounts.Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           "invited@example.com",
		Role:            "member",
		TokenHash:       tokenHash,
		ExpiresAt:       time.Now().Add(72 * time.Hour),
		InvitedByUserID: ownerUserID,
	}

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "different@example.com",
		EmailVerified: true,
	}

	_, err := svc.AcceptInvitation(context.Background(), viewer, rawToken)
	if err == nil {
		t.Fatal("expected error for email mismatch, got nil")
	}
}
