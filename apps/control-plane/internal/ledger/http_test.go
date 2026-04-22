package ledger

import (
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
