package budgets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

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

func TestGetBudgetRejectsUnverifiedViewer(t *testing.T) {
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
		EmailVerified: false,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/budget", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}
