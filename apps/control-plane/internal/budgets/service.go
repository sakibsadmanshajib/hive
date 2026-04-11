package budgets

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Service provides budget threshold management and alert notification.
type Service struct {
	repo     ThresholdRepository
	notifier EmailNotifier
	logger   *slog.Logger
}

// NewService creates a new Service with the given repository and email notifier.
func NewService(repo ThresholdRepository, notifier EmailNotifier) *Service {
	return &Service{
		repo:     repo,
		notifier: notifier,
		logger:   slog.Default(),
	}
}

// GetThreshold returns the budget threshold for the given account, or nil if none is set.
func (s *Service) GetThreshold(ctx context.Context, accountID uuid.UUID) (*BudgetThreshold, error) {
	t, err := s.repo.GetThreshold(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("budgets: get threshold: %w", err)
	}
	return t, nil
}

// UpsertThreshold creates or updates the budget threshold for the given account.
func (s *Service) UpsertThreshold(ctx context.Context, accountID uuid.UUID, input UpsertThresholdInput) (*BudgetThreshold, error) {
	if input.ThresholdCredits <= 0 {
		return nil, &ValidationError{Field: "threshold_credits", Message: "threshold_credits must be greater than zero"}
	}
	t, err := s.repo.UpsertThreshold(ctx, accountID, input.ThresholdCredits)
	if err != nil {
		return nil, fmt.Errorf("budgets: upsert threshold: %w", err)
	}
	return t, nil
}

// DismissAlert dismisses the budget alert for the given account.
func (s *Service) DismissAlert(ctx context.Context, accountID uuid.UUID) error {
	if err := s.repo.DismissAlert(ctx, accountID); err != nil {
		return fmt.Errorf("budgets: dismiss alert: %w", err)
	}
	return nil
}

// CheckThresholds evaluates the current balance against the account's threshold and
// sends a budget alert email when the balance drops below the threshold and the
// alert has not been dismissed or recently sent (within 24h).
// Notification failure is non-fatal and is logged without returning an error.
func (s *Service) CheckThresholds(ctx context.Context, accountID uuid.UUID, currentBalance int64) error {
	threshold, err := s.repo.GetThreshold(ctx, accountID)
	if err != nil {
		return fmt.Errorf("budgets: check thresholds: %w", err)
	}
	if threshold == nil {
		return nil
	}

	if currentBalance > threshold.ThresholdCredits || threshold.AlertDismissed {
		return nil
	}

	// Check 24-hour notification cooldown.
	if threshold.LastNotifiedAt != nil && time.Since(*threshold.LastNotifiedAt) < 24*time.Hour {
		return nil
	}

	s.logger.InfoContext(ctx, "budget threshold breached",
		"account_id", accountID,
		"threshold_credits", threshold.ThresholdCredits,
		"current_balance", currentBalance,
	)

	if err := s.notifier.SendBudgetAlert(ctx, accountID, *threshold, currentBalance); err != nil {
		s.logger.ErrorContext(ctx, "budget alert email failed",
			"account_id", accountID,
			"error", err,
		)
		// Non-fatal: do not block caller on notification failure.
		return nil
	}

	if err := s.repo.MarkNotified(ctx, accountID); err != nil {
		s.logger.ErrorContext(ctx, "mark notified failed after budget alert",
			"account_id", accountID,
			"error", err,
		)
	}

	return nil
}

// ValidationError is a field-level validation error returned by service methods.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}
