package profiles

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

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
