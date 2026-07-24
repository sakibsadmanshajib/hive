package profiles

import (
	"bytes"
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
	profileSvc := NewService(repo)
	accountsSvc := accounts.NewService(repo)
	return NewHandler(profileSvc, accountsSvc)
}

func TestCurrentAccountProfileHandlerReturnsCurrentProfile(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	userID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "acme-labs",
		DisplayName: "Acme Labs",
		AccountType: "business",
		OwnerUserID: userID,
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
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "alice@example.com",
		EmailVerified: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/profile", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var profile AccountProfile
	if err := json.Unmarshal(rr.Body.Bytes(), &profile); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if profile.DisplayName != "Acme Labs" {
		t.Fatalf("expected display_name %q, got %q", "Acme Labs", profile.DisplayName)
	}
	if !profile.ProfileSetupComplete {
		t.Fatal("expected profile_setup_complete to be true")
	}
}

func TestCurrentAccountProfileHandlerPersistsCoreFields(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	userID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "legacy-workspace",
		DisplayName: "Legacy Workspace",
		AccountType: "personal",
		OwnerUserID: userID,
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Legacy Workspace",
		AccountType:          "personal",
		ProfileSetupComplete: false,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "alice@example.com",
		EmailVerified: true,
	}

	body, err := json.Marshal(UpdateAccountProfileInput{
		OwnerName:   "Alice Smith",
		LoginEmail:  "alice@example.com",
		DisplayName: "Acme Labs",
		AccountType: "business",
		CountryCode: "US",
		StateRegion: "CA",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/current/profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var profile AccountProfile
	if err := json.Unmarshal(rr.Body.Bytes(), &profile); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if profile.OwnerName != "Alice Smith" {
		t.Fatalf("expected owner_name %q, got %q", "Alice Smith", profile.OwnerName)
	}
	if repo.accountsMap[accountID].DisplayName != "Acme Labs" {
		t.Fatalf("expected accounts.display_name %q, got %q", "Acme Labs", repo.accountsMap[accountID].DisplayName)
	}
}

func TestCurrentAccountProfileHandlerRejectsInvalidAccountType(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	userID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "legacy-workspace",
		DisplayName: "Legacy Workspace",
		AccountType: "personal",
		OwnerUserID: userID,
	}
	repo.profiles[accountID] = AccountProfile{
		OwnerName:            "Alice",
		LoginEmail:           "alice@example.com",
		DisplayName:          "Legacy Workspace",
		AccountType:          "personal",
		ProfileSetupComplete: false,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "alice@example.com",
		EmailVerified: true,
	}

	body, err := json.Marshal(UpdateAccountProfileInput{
		OwnerName:   "Alice Smith",
		LoginEmail:  "alice@example.com",
		DisplayName: "Acme Labs",
		AccountType: "team",
		CountryCode: "US",
		StateRegion: "CA",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/current/profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if resp["error"] == "" {
		t.Fatal("expected error message in response body")
	}
}

func TestCurrentAccountBillingProfileHandlerPersistsPartialBusinessFields(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	userID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "acme-labs",
		DisplayName: "Acme Labs",
		AccountType: "business",
		OwnerUserID: userID,
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
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "alice@example.com",
		EmailVerified: true,
	}

	body, err := json.Marshal(UpdateBillingProfileInput{
		LegalEntityName: "Acme Labs LLC",
		LegalEntityType: "private_company",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/current/billing-profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var profile BillingProfile
	if err := json.Unmarshal(rr.Body.Bytes(), &profile); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if profile.LegalEntityName != "Acme Labs LLC" {
		t.Fatalf("expected legal_entity_name %q, got %q", "Acme Labs LLC", profile.LegalEntityName)
	}
	if profile.VATNumber != "" {
		t.Fatalf("expected vat_number to remain empty, got %q", profile.VATNumber)
	}
}

func TestCurrentAccountBillingProfileHandlerDefaultsPersonalEntityType(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	userID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "alice-workspace",
		DisplayName: "Alice Workspace",
		AccountType: "personal",
		OwnerUserID: userID,
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
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "alice@example.com",
		EmailVerified: true,
	}

	body, err := json.Marshal(UpdateBillingProfileInput{
		BillingContactName:  "Alice Smith",
		BillingContactEmail: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/current/billing-profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var profile BillingProfile
	if err := json.Unmarshal(rr.Body.Bytes(), &profile); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if profile.LegalEntityType != "individual" {
		t.Fatalf("expected legal_entity_type %q, got %q", "individual", profile.LegalEntityType)
	}
}

func TestCurrentAccountBillingProfileHandlerRejectsUnverifiedViewer(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	userID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "acme-labs",
		DisplayName: "Acme Labs",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "alice@example.com",
		EmailVerified: false,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/billing-profile", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandler_ProfilesAuthzMatrix verifies the Phase 18 permission matrix for
// billing-profile endpoints: workspace.settings requires verified owner.
func TestHandler_ProfilesAuthzMatrix(t *testing.T) {
	cases := []struct {
		name       string
		role       string
		verified   bool
		wantStatus int
	}{
		{"owner verified", "owner", true, http.StatusNotFound}, // 404 = no profile stored, but authz passed
		{"owner unverified", "owner", false, http.StatusForbidden},
		{"member verified", "member", true, http.StatusForbidden},
		{"member unverified", "member", false, http.StatusForbidden},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			accountID := uuid.New()
			userID := uuid.New()

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

			req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/billing-profile", nil)
			req = req.WithContext(viewerCtx(viewer))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("want %d got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestBillingProfile_PlatformAdminOverlayGrantsAccess is a regression guard
// for issue #424: resolveVerifiedCurrentAccountID hardcoded isAdmin=false
// when building the Actor, so a real platform admin who is neither a
// workspace owner nor account-verified was silently denied billing-profile
// access even though the admin overlay should grant it. A hardcoded-false
// version returns 403 (per TestHandler_ProfilesAuthzMatrix's "member
// unverified" case); the fix must pass authz and reach the not-found path
// (404, no profile stored), matching the "owner verified" case's behavior.
func TestBillingProfile_PlatformAdminOverlayGrantsAccess(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	userID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "ws",
		DisplayName: "WS",
		AccountType: "personal",
		OwnerUserID: uuid.New(),
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "member", Status: "active"},
	}

	roleSvc := platform.NewRoleService(&stubRoleStore{adminUsers: map[uuid.UUID]bool{userID: true}})
	profileSvc := NewService(repo)
	accountsSvc := accounts.NewService(repo)
	handler := NewHandler(profileSvc, accountsSvc).WithRoleService(roleSvc)

	viewer := auth.Viewer{UserID: userID, Email: "admin@example.com", EmailVerified: false}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/billing-profile", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (authz passed, no profile stored) for platform admin overlay, got %d: %s", rr.Code, rr.Body.String())
	}
}
