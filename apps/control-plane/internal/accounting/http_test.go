package accounting

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
)

type accountsRepoStub struct {
	accountsMap  map[uuid.UUID]*accounts.Account
	memberships  []accounts.Membership
	invitations  map[string]*accounts.Invitation
}

func newAccountsRepoStub() *accountsRepoStub {
	return &accountsRepoStub{
		accountsMap: make(map[uuid.UUID]*accounts.Account),
		invitations: make(map[string]*accounts.Invitation),
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

func (s *accountsRepoStub) CreateInvitation(_ context.Context, invitation accounts.Invitation) error {
	s.invitations[invitation.TokenHash] = &invitation
	return nil
}

func (s *accountsRepoStub) FindInvitationByTokenHash(_ context.Context, tokenHash string) (*accounts.Invitation, error) {
	invitation, ok := s.invitations[tokenHash]
	if !ok {
		return nil, accounts.ErrNotFound
	}
	return invitation, nil
}

func (s *accountsRepoStub) AcceptInvitation(_ context.Context, invitationID uuid.UUID, acceptedAt time.Time) error {
	for _, invitation := range s.invitations {
		if invitation.ID == invitationID {
			invitation.AcceptedAt = &acceptedAt
			return nil
		}
	}
	return accounts.ErrNotFound
}

func (s *accountsRepoStub) ListMembersByAccountID(_ context.Context, accountID uuid.UUID) ([]accounts.Member, error) {
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

func viewerCtx(viewer auth.Viewer) context.Context {
	return auth.WithViewer(context.Background(), viewer)
}

func newHTTPHandler(accountRepo *accountsRepoStub, accountingRepo *repoStub, ledgerSvc *ledgerStub, usageSvc *usageStub) http.Handler {
	accountsSvc := accounts.NewService(accountRepo)
	accountingSvc := NewService(accountingRepo, ledgerSvc, usageSvc)
	return NewHandler(accountingSvc, accountsSvc)
}

func TestCreateReservationUsesCurrentAccount(t *testing.T) {
	accountRepo := newAccountsRepoStub()
	accountingRepo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledgerBalance(500)}
	usageSvc := &usageStub{}

	userID := uuid.New()
	accountOneID := uuid.New()
	accountTwoID := uuid.New()
	accountRepo.accountsMap[accountOneID] = &accounts.Account{
		ID:          accountOneID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	accountRepo.accountsMap[accountTwoID] = &accounts.Account{
		ID:          accountTwoID,
		Slug:        "workspace-two",
		DisplayName: "Workspace Two",
		AccountType: "business",
		OwnerUserID: userID,
	}
	accountRepo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountOneID, UserID: userID, Role: "owner", Status: "active"},
		{ID: uuid.New(), AccountID: accountTwoID, UserID: userID, Role: "owner", Status: "active"},
	}

	handler := newHTTPHandler(accountRepo, accountingRepo, ledgerSvc, usageSvc)
	viewer := auth.Viewer{UserID: userID, Email: "owner@example.com", EmailVerified: true}

	body, err := json.Marshal(map[string]any{
		"request_id":        "req_http_create",
		"attempt_number":    1,
		"endpoint":          "/v1/responses",
		"model_alias":       "hive-fast",
		"estimated_credits": 120,
		"policy_mode":       string(PolicyModeStrict),
		"customer_tags":     map[string]any{"project": "demo"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/credits/reservations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hive-Account-ID", accountTwoID.String())
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var reservation Reservation
	if err := json.Unmarshal(rr.Body.Bytes(), &reservation); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	if reservation.AccountID != accountTwoID {
		t.Fatalf("expected reservation for current account %s, got %s", accountTwoID, reservation.AccountID)
	}
	if len(usageSvc.startCalls) != 1 || usageSvc.startCalls[0].AccountID != accountTwoID {
		t.Fatalf("expected reservation creation to use current account %s, got %#v", accountTwoID, usageSvc.startCalls)
	}
}

func TestFinalizeReservationRequiresReservationID(t *testing.T) {
	accountRepo, handler, viewer := newFinalizeHandler(t)

	_ = accountRepo

	body, err := json.Marshal(map[string]any{
		"actual_credits":           40,
		"terminal_usage_confirmed": true,
		"status":                   "completed",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/credits/reservations/finalize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestReleaseReservationReturnsPolicyError(t *testing.T) {
	accountRepo, accountingRepo, handler, viewer, accountID := newReleaseHandler(t)
	_ = accountRepo

	reservationID := uuid.New()
	accountingRepo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		ReservationKey:   "req_finalized:1",
		RequestID:        "req_finalized",
		AttemptNumber:    1,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusFinalized,
		ReservedCredits:  75,
	}

	body, err := json.Marshal(map[string]any{
		"reservation_id": reservationID.String(),
		"reason":         "cancelled",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/credits/reservations/release", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestExpandReservationRejectsInvalidUUID(t *testing.T) {
	_, handler, viewer := newFinalizeHandler(t)

	body, err := json.Marshal(map[string]any{
		"reservation_id":    "not-a-uuid",
		"additional_credits": 50,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/credits/reservations/expand", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func newFinalizeHandler(t *testing.T) (*accountsRepoStub, http.Handler, auth.Viewer) {
	t.Helper()

	accountRepo := newAccountsRepoStub()
	accountingRepo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledgerBalance(500)}
	usageSvc := &usageStub{}

	userID := uuid.New()
	accountID := uuid.New()
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

	handler := newHTTPHandler(accountRepo, accountingRepo, ledgerSvc, usageSvc)
	viewer := auth.Viewer{UserID: userID, Email: "owner@example.com", EmailVerified: true}
	return accountRepo, handler, viewer
}

func newReleaseHandler(t *testing.T) (*accountsRepoStub, *repoStub, http.Handler, auth.Viewer, uuid.UUID) {
	t.Helper()

	accountRepo := newAccountsRepoStub()
	accountingRepo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledgerBalance(500)}
	usageSvc := &usageStub{}

	userID := uuid.New()
	accountID := uuid.New()
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

	handler := newHTTPHandler(accountRepo, accountingRepo, ledgerSvc, usageSvc)
	viewer := auth.Viewer{UserID: userID, Email: "owner@example.com", EmailVerified: true}
	return accountRepo, accountingRepo, handler, viewer, accountID
}

func ledgerBalance(available int64) ledger.BalanceSummary {
	return ledger.BalanceSummary{AvailableCredits: available}
}
