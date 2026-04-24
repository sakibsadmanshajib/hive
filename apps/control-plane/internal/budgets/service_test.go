package budgets_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/budgets"
)

// mockRepo implements ThresholdRepository for testing.
type mockRepo struct {
	threshold *budgets.BudgetThreshold
	notified  bool
}

func (m *mockRepo) GetThreshold(_ context.Context, _ uuid.UUID) (*budgets.BudgetThreshold, error) {
	return m.threshold, nil
}

func (m *mockRepo) UpsertThreshold(_ context.Context, _ uuid.UUID, _ int64) (*budgets.BudgetThreshold, error) {
	return m.threshold, nil
}

func (m *mockRepo) DismissAlert(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (m *mockRepo) MarkNotified(_ context.Context, _ uuid.UUID) error {
	m.notified = true
	return nil
}

// mockNotifier tracks whether SendBudgetAlert was called.
type mockNotifier struct {
	called   bool
	lastAcct uuid.UUID
}

func (m *mockNotifier) SendBudgetAlert(_ context.Context, accountID uuid.UUID, _ budgets.BudgetThreshold, _ int64) error {
	m.called = true
	m.lastAcct = accountID
	return nil
}

func TestCheckThresholds_SendsEmailWhenBalanceBelowThreshold(t *testing.T) {
	acctID := uuid.New()
	repo := &mockRepo{
		threshold: &budgets.BudgetThreshold{
			ID:               uuid.New(),
			AccountID:        acctID,
			ThresholdCredits: 100000,
			AlertDismissed:   false,
			LastNotifiedAt:   nil, // never notified
		},
	}
	notifier := &mockNotifier{}
	svc := budgets.NewService(repo, notifier)

	err := svc.CheckThresholds(context.Background(), acctID, 50000) // balance below threshold
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !notifier.called {
		t.Error("expected email notification to be sent when balance is below threshold")
	}
	if !repo.notified {
		t.Error("expected MarkNotified to be called after successful email send")
	}
}

func TestCheckThresholds_NoEmailWhenBalanceAboveThreshold(t *testing.T) {
	acctID := uuid.New()
	repo := &mockRepo{
		threshold: &budgets.BudgetThreshold{
			ID:               uuid.New(),
			AccountID:        acctID,
			ThresholdCredits: 100000,
			AlertDismissed:   false,
		},
	}
	notifier := &mockNotifier{}
	svc := budgets.NewService(repo, notifier)

	err := svc.CheckThresholds(context.Background(), acctID, 200000) // balance above threshold
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifier.called {
		t.Error("expected no email when balance is above threshold")
	}
}

func TestCheckThresholds_NoEmailWhenDismissed(t *testing.T) {
	acctID := uuid.New()
	repo := &mockRepo{
		threshold: &budgets.BudgetThreshold{
			ID:               uuid.New(),
			AccountID:        acctID,
			ThresholdCredits: 100000,
			AlertDismissed:   true, // dismissed
		},
	}
	notifier := &mockNotifier{}
	svc := budgets.NewService(repo, notifier)

	err := svc.CheckThresholds(context.Background(), acctID, 50000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifier.called {
		t.Error("expected no email when alert is dismissed")
	}
}

func TestCheckThresholds_NoEmailWhenRecentlyNotified(t *testing.T) {
	acctID := uuid.New()
	recentTime := time.Now().Add(-1 * time.Hour) // 1 hour ago, within 24h window
	repo := &mockRepo{
		threshold: &budgets.BudgetThreshold{
			ID:               uuid.New(),
			AccountID:        acctID,
			ThresholdCredits: 100000,
			AlertDismissed:   false,
			LastNotifiedAt:   &recentTime,
		},
	}
	notifier := &mockNotifier{}
	svc := budgets.NewService(repo, notifier)

	err := svc.CheckThresholds(context.Background(), acctID, 50000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifier.called {
		t.Error("expected no email when recently notified (within 24h)")
	}
}

func TestCheckThresholds_NoThresholdSet(t *testing.T) {
	acctID := uuid.New()
	repo := &mockRepo{threshold: nil} // no threshold configured
	notifier := &mockNotifier{}
	svc := budgets.NewService(repo, notifier)

	err := svc.CheckThresholds(context.Background(), acctID, 50000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifier.called {
		t.Error("expected no email when no threshold is configured")
	}
}
