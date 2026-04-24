package accounts_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// --- helpers ---

func viewerCtx(viewer auth.Viewer) context.Context {
	return auth.WithViewer(context.Background(), viewer)
}

func newHandler(repo accounts.Repository) http.Handler {
	svc := accounts.NewService(repo)
	return accounts.NewHandler(svc)
}

// --- GET /api/v1/viewer ---

func TestViewerHandler_ReturnsViewerContext(t *testing.T) {
	repo := newStubRepo()
	h := newHandler(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "owner@example.com",
		EmailVerified: true,
		FullName:      "Test Owner",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/viewer", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := resp["user"]; !ok {
		t.Error("response missing 'user' field")
	}
	if _, ok := resp["current_account"]; !ok {
		t.Error("response missing 'current_account' field")
	}
	if _, ok := resp["gates"]; !ok {
		t.Error("response missing 'gates' field")
	}
}

func TestViewerHandler_AcceptsXHiveAccountIDHeader(t *testing.T) {
	repo := newStubRepo()

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

	h := newHandler(repo)

	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "user@example.com",
		EmailVerified: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/viewer", nil)
	req.Header.Set("X-Hive-Account-ID", acct2ID.String())
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	currentAcct, ok := resp["current_account"].(map[string]interface{})
	if !ok {
		t.Fatal("current_account is not an object")
	}
	if currentAcct["id"] != acct2ID.String() {
		t.Errorf("expected current_account.id=%v, got %v", acct2ID, currentAcct["id"])
	}
}

func TestViewerHandler_InvalidXHiveAccountIDFallsBack(t *testing.T) {
	repo := newStubRepo()

	userID := uuid.New()
	acct1ID := uuid.New()

	acct1 := accounts.Account{ID: acct1ID, Slug: "my-workspace", DisplayName: "My Workspace", AccountType: "personal", OwnerUserID: userID}
	repo.accountsMap[acct1ID] = &acct1
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: acct1ID, UserID: userID, Role: "owner", Status: "active"},
	}

	h := newHandler(repo)

	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "user@example.com",
		EmailVerified: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/viewer", nil)
	req.Header.Set("X-Hive-Account-ID", uuid.New().String()) // unknown account
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	currentAcct, ok := resp["current_account"].(map[string]interface{})
	if !ok {
		t.Fatal("current_account is not an object")
	}
	if currentAcct["id"] != acct1ID.String() {
		t.Errorf("expected fallback to account %v, got %v", acct1ID, currentAcct["id"])
	}
}

// --- POST /api/v1/accounts/current/invitations ---

func TestInvitationHandler_UnverifiedReturns403(t *testing.T) {
	repo := newStubRepo()

	ownerUserID := uuid.New()
	accountID := uuid.New()

	acct := accounts.Account{
		ID:          accountID,
		Slug:        "my-workspace",
		DisplayName: "My Workspace",
		AccountType: "personal",
		OwnerUserID: ownerUserID,
	}
	repo.accountsMap[accountID] = &acct
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: ownerUserID, Role: "owner", Status: "active"},
	}

	h := newHandler(repo)

	viewer := auth.Viewer{
		UserID:        ownerUserID,
		Email:         "owner@example.com",
		EmailVerified: false,
	}

	// We need the viewer to have a current account set.
	// Pre-populate viewer context with the account selection.
	ctx := auth.WithViewer(context.Background(), viewer)

	body := `{"email":"invitee@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/invitations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hive-Account-ID", accountID.String())
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["code"] != "email_verification_required" {
		t.Errorf("expected code=email_verification_required, got %v", resp["code"])
	}
}

func TestInvitationHandler_VerifiedOwnerCreatesInvitation(t *testing.T) {
	repo := newStubRepo()

	ownerUserID := uuid.New()
	accountID := uuid.New()

	acct := accounts.Account{
		ID:          accountID,
		Slug:        "my-workspace",
		DisplayName: "My Workspace",
		AccountType: "personal",
		OwnerUserID: ownerUserID,
	}
	repo.accountsMap[accountID] = &acct
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: ownerUserID, Role: "owner", Status: "active"},
	}

	h := newHandler(repo)

	viewer := auth.Viewer{
		UserID:        ownerUserID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}

	ctx := auth.WithViewer(context.Background(), viewer)

	body := `{"email":"invitee@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/invitations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hive-Account-ID", accountID.String())
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["token"] == "" || resp["token"] == nil {
		t.Error("expected invitation token in response")
	}
}

func TestMembersHandler_UnverifiedReturns403(t *testing.T) {
	repo := newStubRepo()

	userID := uuid.New()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "restricted-workspace",
		DisplayName: "Restricted Workspace",
		AccountType: "personal",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	h := newHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "unverified@example.com",
		EmailVerified: false,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/members", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- POST /api/v1/invitations/accept ---

func TestAcceptInvitationHandler(t *testing.T) {
	repo := newStubRepo()

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

	rawToken := "accept-test-token"
	tokenHash := accounts.HashToken(rawToken)
	repo.invitations[tokenHash] = &accounts.Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           "invitee@example.com",
		Role:            "member",
		TokenHash:       tokenHash,
		ExpiresAt:       time.Now().Add(72 * time.Hour),
		InvitedByUserID: ownerUserID,
	}

	h := newHandler(repo)

	viewer := auth.Viewer{
		UserID:        inviteeUserID,
		Email:         "invitee@example.com",
		EmailVerified: true,
	}

	ctx := auth.WithViewer(context.Background(), viewer)
	body := `{"token":"accept-test-token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["account_id"] == nil {
		t.Error("expected account_id in accept response")
	}
}
