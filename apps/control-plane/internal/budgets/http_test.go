package budgets

import (
	"bytes"
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// stubRoleStore is a minimal platform.RoleStore backing a real
// *platform.RoleService for tests. adminUsers drives IsPlatformAdmin;
// owners drives GetMembershipRole (and therefore IsWorkspaceOwner) — any
// (userID, workspaceID) pair not present in owners resolves as a non-owner
// member (never ErrWorkspaceNotFound).
type stubRoleStore struct {
	adminUsers map[uuid.UUID]bool
	owners     map[uuid.UUID]uuid.UUID // workspaceID -> owner userID
}

func (s *stubRoleStore) GetMembershipRole(_ context.Context, userID, workspaceID uuid.UUID) (platform.MembershipRole, error) {
	if ownerID, ok := s.owners[workspaceID]; ok && ownerID == userID {
		return platform.RoleOwner, nil
	}
	return platform.RoleMember, nil
}

func (s *stubRoleStore) IsPlatformAdmin(_ context.Context, userID uuid.UUID) (bool, error) {
	return s.adminUsers[userID], nil
}

// workspaceRepoStub is a minimal in-memory WorkspaceBudgetRepository —
// only GetBudget/UpsertBudget are exercised by the platform-admin overlay
// regression tests below.
type workspaceRepoStub struct{}

func (s *workspaceRepoStub) GetBudget(_ context.Context, _ uuid.UUID) (*Budget, error) {
	return nil, nil
}

func (s *workspaceRepoStub) UpsertBudget(_ context.Context, in SetBudgetInput) (*Budget, error) {
	return &Budget{
		WorkspaceID: in.WorkspaceID,
		PeriodStart: in.PeriodStart,
		SoftCap:     in.SoftCap,
		HardCap:     in.HardCap,
		Currency:    "BDT",
	}, nil
}

func (s *workspaceRepoStub) DeleteBudget(_ context.Context, _ uuid.UUID) error { return nil }

func (s *workspaceRepoStub) ListAlerts(_ context.Context, _ uuid.UUID) ([]SpendAlert, error) {
	return nil, nil
}

func (s *workspaceRepoStub) CreateAlert(_ context.Context, _ CreateAlertInput) (*SpendAlert, error) {
	return nil, nil
}

func (s *workspaceRepoStub) UpdateAlert(_ context.Context, _ UpdateAlertInput) (*SpendAlert, error) {
	return nil, nil
}

func (s *workspaceRepoStub) DeleteAlert(_ context.Context, _, _ uuid.UUID) error { return nil }

func (s *workspaceRepoStub) ListWorkspacesWithBudget(_ context.Context) ([]uuid.UUID, error) {
	return nil, nil
}

func (s *workspaceRepoStub) StampAlertFired(_ context.Context, _ uuid.UUID, _, _ time.Time) error {
	return nil
}

func (s *workspaceRepoStub) MonthToDateSpendBDT(_ context.Context, _ uuid.UUID, _ time.Time) (*big.Int, error) {
	return big.NewInt(0), nil
}

type httpRepoStub struct{}

func (s *httpRepoStub) GetThreshold(_ context.Context, _ uuid.UUID) (*BudgetThreshold, error) {
	return nil, nil
}

func (s *httpRepoStub) UpsertThreshold(_ context.Context, _ uuid.UUID, _ int64) (*BudgetThreshold, error) {
	return nil, nil
}

func (s *httpRepoStub) DismissAlert(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (s *httpRepoStub) MarkNotified(_ context.Context, _ uuid.UUID) error {
	return nil
}

type notifierStub struct{}

func (n *notifierStub) SendBudgetAlert(_ context.Context, _ uuid.UUID, _ BudgetThreshold, _ int64) error {
	return nil
}

type accountsRepoStub struct {
	accountsMap map[uuid.UUID]*accounts.Account
	memberships []accounts.Membership
}

func newAccountsRepoStub() *accountsRepoStub {
	return &accountsRepoStub{
		accountsMap: make(map[uuid.UUID]*accounts.Account),
	}
}

func (s *accountsRepoStub) ListMembershipsByUserID(_ context.Context, userID uuid.UUID) ([]accounts.Membership, error) {
	var memberships []accounts.Membership
	for _, membership := range s.memberships {
		if membership.UserID == userID {
			memberships = append(memberships, membership)
		}
	}
	return memberships, nil
}

func (s *accountsRepoStub) CreateAccount(_ context.Context, acct accounts.Account) error {
	s.accountsMap[acct.ID] = &acct
	return nil
}

func (s *accountsRepoStub) CreateMembership(_ context.Context, membership accounts.Membership) error {
	s.memberships = append(s.memberships, membership)
	return nil
}

func (s *accountsRepoStub) CreateProfile(_ context.Context, _ accounts.AccountProfile) error {
	return nil
}

func (s *accountsRepoStub) GetAccountByID(_ context.Context, id uuid.UUID) (*accounts.Account, error) {
	acct, ok := s.accountsMap[id]
	if !ok {
		return nil, accounts.ErrNotFound
	}
	return acct, nil
}

func (s *accountsRepoStub) CreateInvitation(_ context.Context, _ accounts.Invitation) error {
	return nil
}

func (s *accountsRepoStub) FindInvitationByTokenHash(_ context.Context, _ string) (*accounts.Invitation, error) {
	return nil, accounts.ErrNotFound
}

func (s *accountsRepoStub) AcceptInvitation(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return accounts.ErrNotFound
}

func (s *accountsRepoStub) ListMembersByAccountID(_ context.Context, _ uuid.UUID) ([]accounts.Member, error) {
	return nil, nil
}

func viewerCtx(viewer auth.Viewer) context.Context {
	return auth.WithViewer(context.Background(), viewer)
}

// TestHandler_BudgetAuthzMatrix verifies the Phase 18 permission matrix for
// billing endpoints: billing.view (RequiresVerified=false) and billing.write
// (RequiresVerified=true, owner-only).
func TestHandler_BudgetAuthzMatrix(t *testing.T) {
	cases := []struct {
		name       string
		role       string
		verified   bool
		method     string
		path       string
		wantStatus int
	}{
		// billing.view — RequiresVerified=false — unverified owner allowed
		{"owner unverified view budget", "owner", false, http.MethodGet, "/api/v1/accounts/current/budget", http.StatusOK},
		{"owner verified view budget", "owner", true, http.MethodGet, "/api/v1/accounts/current/budget", http.StatusOK},
		// member cannot view budget (not granted billing.view)
		{"member verified view budget", "member", true, http.MethodGet, "/api/v1/accounts/current/budget", http.StatusForbidden},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			accountRepo := newAccountsRepoStub()
			userID := uuid.New()
			accountID := uuid.New()

			accountRepo.accountsMap[accountID] = &accounts.Account{
				ID:          accountID,
				Slug:        "ws",
				DisplayName: "WS",
				AccountType: "personal",
				OwnerUserID: userID,
			}
			accountRepo.memberships = []accounts.Membership{
				{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: tc.role, Status: "active"},
			}

			handler := NewHandler(NewService(&httpRepoStub{}, &notifierStub{}), accounts.NewService(accountRepo))
			viewer := auth.Viewer{UserID: userID, Email: "u@example.com", EmailVerified: tc.verified}

			req := httptest.NewRequest(tc.method, tc.path, nil)
			req = req.WithContext(viewerCtx(viewer))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("want %d got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestGetBudgetAllowsUnverifiedOwner verifies that the Phase 18 matrix allows
// unverified owners to view the budget. billing.view has RequiresVerified=false
// per the permission registry, so unverified owners must get 200 (not 403).
// This replaces the pre-Phase-18 test that checked !EmailVerified.
func TestGetBudgetAllowsUnverifiedOwner(t *testing.T) {
	accountRepo := newAccountsRepoStub()
	accountID := uuid.New()
	userID := uuid.New()

	accountRepo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	accountRepo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := NewHandler(NewService(&httpRepoStub{}, &notifierStub{}), accounts.NewService(accountRepo))
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: false, // unverified owner — billing.view is RequiresVerified=false
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/budget", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Phase 18: billing.view RequiresVerified=false → unverified owner gets 200.
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for unverified owner (billing.view requires no verification), got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetBudget_PlatformAdminOverlayGrantsAccess is a regression guard for
// issue #424: resolveCurrentAccountID hardcoded isAdmin=false when building
// the Actor, so a real platform admin who is a non-owner member was silently
// denied billing.view access (PermBillingView is owner-only) even though the
// admin overlay should grant it. A hardcoded-false version returns 403 here;
// the fix must return 200.
func TestGetBudget_PlatformAdminOverlayGrantsAccess(t *testing.T) {
	accountRepo := newAccountsRepoStub()
	userID := uuid.New()
	accountID := uuid.New()

	accountRepo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "ws",
		DisplayName: "WS",
		AccountType: "personal",
		OwnerUserID: uuid.New(),
	}
	accountRepo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "member", Status: "active"},
	}

	roleSvc := platform.NewRoleService(&stubRoleStore{adminUsers: map[uuid.UUID]bool{userID: true}})
	handler := NewHandler(NewService(&httpRepoStub{}, &notifierStub{}), accounts.NewService(accountRepo)).WithRoleService(roleSvc)

	viewer := auth.Viewer{UserID: userID, Email: "admin@example.com", EmailVerified: true}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/budget", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for platform admin overlay, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestWorkspaceBudgetPUT_PlatformAdminOverlayGrantsAccess is a regression
// guard for issue #424: requireWorkspaceOwner hardcoded isAdmin=false when
// building the Actor, so a real platform admin who does not own the target
// workspace was silently denied PUT /api/v1/budgets/{ws} even though the
// admin overlay should grant it. A hardcoded-false version returns 403 here;
// the fix must return 200.
func TestWorkspaceBudgetPUT_PlatformAdminOverlayGrantsAccess(t *testing.T) {
	userID := uuid.New()
	workspaceID := uuid.New()

	roleSvc := platform.NewRoleService(&stubRoleStore{
		adminUsers: map[uuid.UUID]bool{userID: true},
		owners:     map[uuid.UUID]uuid.UUID{}, // userID does not own workspaceID
	})
	svc := NewServiceWithWorkspace(&httpRepoStub{}, &notifierStub{}, &workspaceRepoStub{}, nil, nil)
	handler := NewHandler(svc, accounts.NewService(newAccountsRepoStub())).WithRoleService(roleSvc)

	viewer := auth.Viewer{UserID: userID, Email: "admin@example.com", EmailVerified: true}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/budgets/"+workspaceID.String(),
		bytes.NewBufferString(`{"soft_cap_bdt_subunits":1000,"hard_cap_bdt_subunits":2000}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for platform admin overlay, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestWorkspaceBudgetGET_PlatformAdminOverlayGrantsAccess is a regression
// guard for issue #424: requireWorkspaceMembership hardcoded isAdmin=false
// when building the Actor, so a real platform admin who is not a member of
// the target workspace was silently denied GET /api/v1/budgets/{ws} even
// though the admin overlay should grant it. A hardcoded-false version
// returns 403 here; the fix must return 200.
func TestWorkspaceBudgetGET_PlatformAdminOverlayGrantsAccess(t *testing.T) {
	userID := uuid.New()
	workspaceID := uuid.New()

	roleSvc := platform.NewRoleService(&stubRoleStore{
		adminUsers: map[uuid.UUID]bool{userID: true},
		owners:     map[uuid.UUID]uuid.UUID{}, // userID is not even a member of workspaceID
	})
	svc := NewServiceWithWorkspace(&httpRepoStub{}, &notifierStub{}, &workspaceRepoStub{}, nil, nil)
	handler := NewHandler(svc, accounts.NewService(newAccountsRepoStub())).WithRoleService(roleSvc)

	viewer := auth.Viewer{UserID: userID, Email: "admin@example.com", EmailVerified: true}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/budgets/"+workspaceID.String(), nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for platform admin overlay, got %d: %s", rr.Code, rr.Body.String())
	}
}
