package ledger

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// stubRoleStore is a minimal platform.RoleStore backing a real
// *platform.RoleService for tests, keyed by userID -> is_platform_admin.
type stubRoleStore struct {
	adminUsers map[uuid.UUID]bool
}

func (s *stubRoleStore) GetMembershipRole(_ context.Context, _, _ uuid.UUID) (platform.MembershipRole, error) {
	return "", nil
}

func (s *stubRoleStore) IsPlatformAdmin(_ context.Context, userID uuid.UUID) (bool, error) {
	return s.adminUsers[userID], nil
}

func viewerCtx(viewer auth.Viewer) context.Context {
	return auth.WithViewer(context.Background(), viewer)
}

func newHTTPHandler(repo *stubRepo) http.Handler {
	ledgerSvc := NewService(repo)
	accountsSvc := accounts.NewService(repo)
	return NewHandler(ledgerSvc, accountsSvc)
}

func TestGetBalanceUsesCurrentAccount(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountOneID := uuid.New()
	accountTwoID := uuid.New()

	repo.accountsMap[accountOneID] = &accounts.Account{
		ID:          accountOneID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.accountsMap[accountTwoID] = &accounts.Account{
		ID:          accountTwoID,
		Slug:        "workspace-two",
		DisplayName: "Workspace Two",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountOneID, UserID: userID, Role: "owner", Status: "active"},
		{ID: uuid.New(), AccountID: accountTwoID, UserID: userID, Role: "owner", Status: "active"},
	}

	repo.entries[accountOneID] = []LedgerEntry{{
		ID:             uuid.New(),
		AccountID:      accountOneID,
		EntryType:      EntryTypeGrant,
		CreditsDelta:   100,
		IdempotencyKey: "grant-one",
	}}
	repo.entries[accountTwoID] = []LedgerEntry{{
		ID:             uuid.New(),
		AccountID:      accountTwoID,
		EntryType:      EntryTypeGrant,
		CreditsDelta:   250,
		IdempotencyKey: "grant-two",
	}}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/credits/balance", nil)
	req.Header.Set("X-Hive-Account-ID", accountTwoID.String())
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var balance BalanceSummary
	if err := json.Unmarshal(rr.Body.Bytes(), &balance); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	if balance.AvailableCredits != 250 {
		t.Fatalf("expected balance for current account 250, got %d", balance.AvailableCredits)
	}
}

func TestListLedgerEntriesDefaultsLimit(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	for i := 0; i < 25; i++ {
		repo.entries[accountID] = append(repo.entries[accountID], LedgerEntry{
			ID:             uuid.New(),
			AccountID:      accountID,
			EntryType:      EntryTypeGrant,
			CreditsDelta:   int64(i + 1),
			IdempotencyKey: uuid.NewString(),
		})
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/credits/ledger", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response struct {
		Entries []LedgerEntry `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	if repo.lastListLimit != 20 {
		t.Fatalf("expected default limit 20, got %d", repo.lastListLimit)
	}
	if len(response.Entries) != 20 {
		t.Fatalf("expected 20 ledger entries, got %d", len(response.Entries))
	}
}

func TestGetBalanceRejectsUnverifiedViewer(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: false,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/credits/balance", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandler_LedgerAuthzMatrix verifies the Phase 18 permission matrix for
// ledger endpoints: ledger.view requires verified owner or verified member.
func TestHandler_LedgerAuthzMatrix(t *testing.T) {
	cases := []struct {
		name       string
		role       string
		verified   bool
		wantStatus int
	}{
		{"owner verified", "owner", true, http.StatusOK},
		{"owner unverified", "owner", false, http.StatusForbidden},
		{"member verified", "member", true, http.StatusOK},
		{"member unverified", "member", false, http.StatusForbidden},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			userID := uuid.New()
			accountID := uuid.New()

			repo.accountsMap[accountID] = &accounts.Account{
				ID:          accountID,
				Slug:        "ws",
				DisplayName: "WS",
				AccountType: "personal",
				OwnerUserID: userID,
			}
			repo.memberships = []accounts.Membership{
				{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: tc.role, Status: "active"},
			}

			handler := newHTTPHandler(repo)
			viewer := auth.Viewer{UserID: userID, Email: "u@example.com", EmailVerified: tc.verified}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/credits/balance", nil)
			req = req.WithContext(viewerCtx(viewer))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("want %d got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestGetBalance_PlatformAdminOverlayGrantsUnverifiedAccess is a regression
// guard for issue #424: resolveCurrentAccountID hardcoded isAdmin=false when
// building the Actor, so a real platform admin who is not account-verified
// was silently denied ledger access even though the admin overlay should
// grant it. A hardcoded-false version returns 403 here; the fix must return 200.
func TestGetBalance_PlatformAdminOverlayGrantsUnverifiedAccess(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "member", Status: "active"},
	}

	roleSvc := platform.NewRoleService(&stubRoleStore{adminUsers: map[uuid.UUID]bool{userID: true}})
	ledgerSvc := NewService(repo)
	accountsSvc := accounts.NewService(repo)
	handler := NewHandler(ledgerSvc, accountsSvc).WithRoleService(roleSvc)

	viewer := auth.Viewer{UserID: userID, Email: "admin@example.com", EmailVerified: false}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/credits/balance", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for platform admin overlay, got %d: %s", rr.Code, rr.Body.String())
	}
}
