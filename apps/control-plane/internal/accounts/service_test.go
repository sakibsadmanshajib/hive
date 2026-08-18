package accounts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// --- stub repository ---

type stubRepo struct {
	memberships   []accounts.Membership
	accountsMap   map[uuid.UUID]*accounts.Account
	invitations   map[string]*accounts.Invitation // token -> invitation
	profiles      map[uuid.UUID]*accounts.AccountProfile
	emails        map[uuid.UUID]string    // user id -> auth.users email
	activeTenants map[uuid.UUID]uuid.UUID // user id -> tenant id, for ActiveTenantID
	acceptCalled  bool

	// raceWinnerAccountID, when non-nil, makes ProvisionDefaultWorkspace
	// behave as production does when a concurrent request for the same
	// viewer already committed under the advisory lock: raceWinnerAccount
	// and raceWinnerMembership become visible as part of that call (not
	// before — the outer, unlocked membership check in EnsureViewerContext
	// must still see nothing, exactly like the real TOCTOU window), and the
	// call reports wonElsewhere instead of inserting its own rows. See
	// TestEnsureDefaultAccount_SlugCollisionFromConcurrentSelf_Recovers.
	raceWinnerAccountID  uuid.UUID
	raceWinnerAccount    *accounts.Account
	raceWinnerMembership *accounts.Membership
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		accountsMap:   make(map[uuid.UUID]*accounts.Account),
		invitations:   make(map[string]*accounts.Invitation),
		profiles:      make(map[uuid.UUID]*accounts.AccountProfile),
		emails:        make(map[uuid.UUID]string),
		activeTenants: make(map[uuid.UUID]uuid.UUID),
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

func (s *stubRepo) ActiveTenantID(_ context.Context, userID uuid.UUID) (uuid.UUID, bool, error) {
	tenantID, ok := s.activeTenants[userID]
	return tenantID, ok, nil
}

func (s *stubRepo) CreateAccount(_ context.Context, acct accounts.Account) error {
	s.accountsMap[acct.ID] = &acct
	return nil
}

// ProvisionDefaultWorkspace mirrors pgxRepository's contract (see its doc
// comment in repository.go): recover a concurrent winner if raceWinnerAccountID
// is set, otherwise insert unless the slug collides with an existing account.
func (s *stubRepo) ProvisionDefaultWorkspace(_ context.Context, acct accounts.Account, membership accounts.Membership, profile accounts.AccountProfile) (uuid.UUID, bool, error) {
	if s.raceWinnerAccountID != uuid.Nil {
		s.accountsMap[s.raceWinnerAccount.ID] = s.raceWinnerAccount
		s.memberships = append(s.memberships, *s.raceWinnerMembership)
		return s.raceWinnerAccountID, true, nil
	}
	for _, existing := range s.accountsMap {
		if existing.Slug == acct.Slug {
			return uuid.Nil, false, accounts.ErrSlugTaken
		}
	}
	s.accountsMap[acct.ID] = &acct
	s.memberships = append(s.memberships, membership)
	s.profiles[profile.AccountID] = &profile
	return acct.ID, false, nil
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

// AcceptInvitation mirrors the production statement's accepted_at IS NULL
// predicate: consuming an already consumed invitation matches no row.
func (s *stubRepo) AcceptInvitation(_ context.Context, invitationID uuid.UUID, acceptedAt time.Time) error {
	for _, inv := range s.invitations {
		if inv.ID == invitationID {
			if inv.AcceptedAt != nil {
				return accounts.ErrAlreadyAccepted
			}
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
				Email:  s.emails[m.UserID],
				Role:   m.Role,
				Status: m.Status,
			})
		}
	}
	return members, nil
}

func (s *stubRepo) ActivateMembership(_ context.Context, accountID, userID uuid.UUID, role string) error {
	for i := range s.memberships {
		m := s.memberships[i]
		if m.AccountID == accountID && m.UserID == userID && m.Status == accounts.StatusInvited {
			// Immutable update: replace the row rather than mutating in place.
			updated := m
			updated.Role = role
			updated.Status = accounts.StatusActive
			s.memberships[i] = updated
			return nil
		}
	}
	return accounts.ErrNotFound
}

func (s *stubRepo) UpdateMembershipRole(_ context.Context, accountID, userID uuid.UUID, role string) error {
	for i := range s.memberships {
		m := s.memberships[i]
		if m.AccountID == accountID && m.UserID == userID && m.Status == "active" {
			// Immutable update: replace the row rather than mutating in place.
			updated := m
			updated.Role = role
			s.memberships[i] = updated
			return nil
		}
	}
	return accounts.ErrNotFound
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

func TestEnsureDefaultAccount_UnverifiedPermissionsEmpty(t *testing.T) {
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

	// Unverified actor: permissions requiring verification must be absent.
	for _, perm := range vc.Permissions {
		if perm == "members.invite" || perm == "api_keys.write" {
			t.Errorf("expected %q absent for unverified user, but found it in Permissions", perm)
		}
	}
}

func TestEnsureDefaultAccount_VerifiedOwnerHasKeyPermissions(t *testing.T) {
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

	// Verified owner must hold members.invite and api_keys.write permissions.
	contains := func(perms []string, target string) bool {
		for _, p := range perms {
			if p == target {
				return true
			}
		}
		return false
	}
	if !contains(vc.Permissions, "members.invite") {
		t.Error("expected members.invite in Permissions for verified owner")
	}
	if !contains(vc.Permissions, "api_keys.write") {
		t.Error("expected api_keys.write in Permissions for verified owner")
	}
}

// TestEnsureDefaultAccount_SlugCollisionFromConcurrentSelf_Recovers reproduces
// the CI flake behind "could not load your workspace": Next.js Server
// Components call getViewer() unmemoized per component, so a brand-new
// viewer's very first page load can fire two concurrent
// provisionDefaultWorkspace attempts. repo.ProvisionDefaultWorkspace
// serializes them on a per-viewer advisory lock in production; here the stub
// models the second (losing) call's lock-scoped re-check finding the first's
// committed row, which must be recovered rather than surfaced as a 500 or a
// second, duplicate workspace.
func TestEnsureDefaultAccount_SlugCollisionFromConcurrentSelf_Recovers(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "racer@example.com",
		EmailVerified: true,
		FullName:      "Racer User",
	}

	winningAccountID := uuid.New()
	repo.raceWinnerAccountID = winningAccountID
	repo.raceWinnerAccount = &accounts.Account{
		ID:          winningAccountID,
		Slug:        "racer-user-s-workspace",
		DisplayName: "Racer User's Workspace",
		AccountType: "personal",
		OwnerUserID: viewer.UserID,
	}
	repo.raceWinnerMembership = &accounts.Membership{
		ID:        uuid.New(),
		AccountID: winningAccountID,
		UserID:    viewer.UserID,
		Role:      "owner",
		Status:    "active",
	}

	vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}
	if vc.CurrentAccount.ID != winningAccountID {
		t.Errorf("expected to recover the concurrent winner's account %v, got %v", winningAccountID, vc.CurrentAccount.ID)
	}
	if len(vc.Memberships) != 1 {
		t.Fatalf("expected 1 membership recovered from the race, got %d", len(vc.Memberships))
	}
	// The bug this guards against: a second personal workspace silently
	// created for the same viewer because the losing request didn't see the
	// winner's row yet. Exactly one account must exist for this viewer.
	if len(repo.accountsMap) != 1 {
		t.Fatalf("expected exactly 1 account to exist after the race, got %d", len(repo.accountsMap))
	}
}

// TestEnsureDefaultAccount_SlugCollisionFromDifferentViewer_RetriesWithSuffix
// covers the other trigger for the same unique constraint: two different
// viewers whose display names collapse to the same slug (buildSlug has no
// per-user uniqueifier). Unlike the self-race case, this viewer holds no
// membership after the collision, so provisioning must retry with a
// de-duplicated slug rather than recover a workspace that is not theirs.
func TestEnsureDefaultAccount_SlugCollisionFromDifferentViewer_RetriesWithSuffix(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	otherUserID := uuid.New()
	otherAccountID := uuid.New()
	// buildSlug("Same Name's Workspace") == "same-name-s-workspace" — pinned
	// here to force the collision without exporting the algorithm to this
	// blackbox test package.
	repo.accountsMap[otherAccountID] = &accounts.Account{
		ID:          otherAccountID,
		Slug:        "same-name-s-workspace",
		DisplayName: "Same Name's Workspace",
		AccountType: "personal",
		OwnerUserID: otherUserID,
	}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: otherAccountID,
		UserID:    otherUserID,
		Role:      "owner",
		Status:    "active",
	})

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "samename@example.com",
		EmailVerified: true,
		FullName:      "Same Name",
	}

	vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}
	if vc.CurrentAccount.ID == otherAccountID {
		t.Fatal("expected a distinct account for the colliding different-viewer case, got the other viewer's account")
	}
	if len(vc.Memberships) != 1 {
		t.Fatalf("expected 1 membership for the new viewer, got %d", len(vc.Memberships))
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

	_, err := svc.CreateInvitation(context.Background(), accountID, viewer, "member@example.com", "member")
	if err == nil {
		t.Fatal("expected error for unverified owner, got nil")
	}

	var gateErr *accounts.GateError
	if !accounts.AsGateError(err, &gateErr) {
		t.Fatalf("expected GateError, got %T: %v", err, err)
	}
	// Phase 18: error code migrated from email_verification_required to
	// permission_denied — the policy.Can check is now the single gate.
	if gateErr.Code != "permission_denied" {
		t.Errorf("expected code permission_denied, got %q", gateErr.Code)
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

	inv, err := svc.CreateInvitation(context.Background(), accountID, viewer, "member@example.com", "member")
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
