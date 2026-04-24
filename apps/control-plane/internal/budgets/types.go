package budgets

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// BudgetThreshold represents a spend alert threshold for an account.
type BudgetThreshold struct {
	ID               uuid.UUID  `json:"id"`
	AccountID        uuid.UUID  `json:"account_id"`
	ThresholdCredits int64      `json:"threshold_credits"`
	LastNotifiedAt   *time.Time `json:"last_notified_at"`
	AlertDismissed   bool       `json:"alert_dismissed"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// UpsertThresholdInput is the input for creating or updating a budget threshold.
type UpsertThresholdInput struct {
	ThresholdCredits int64 `json:"threshold_credits"`
}

// ThresholdRepository is the minimal repository interface used by Service.
// Production: *pgxRepository. Tests: mockRepo.
type ThresholdRepository interface {
	GetThreshold(ctx context.Context, accountID uuid.UUID) (*BudgetThreshold, error)
	UpsertThreshold(ctx context.Context, accountID uuid.UUID, credits int64) (*BudgetThreshold, error)
	DismissAlert(ctx context.Context, accountID uuid.UUID) error
	MarkNotified(ctx context.Context, accountID uuid.UUID) error
}

// EmailNotifier defines the interface for sending budget alert emails.
// This is an interface so it can be mocked in tests.
type EmailNotifier interface {
	SendBudgetAlert(ctx context.Context, accountID uuid.UUID, threshold BudgetThreshold, currentBalance int64) error
}
