package budgets

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// LogNotifier is a no-op EmailNotifier that logs budget alerts instead of sending email.
// It is used in development and as a fallback until a real email sender is configured.
type LogNotifier struct {
	logger *slog.Logger
}

// NewLogNotifier creates a LogNotifier using the provided logger.
func NewLogNotifier(logger *slog.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

// SendBudgetAlert logs the budget alert to the configured logger.
func (n *LogNotifier) SendBudgetAlert(ctx context.Context, accountID uuid.UUID, threshold BudgetThreshold, currentBalance int64) error {
	n.logger.WarnContext(ctx, "BUDGET ALERT: balance below threshold (email not configured)",
		"account_id", accountID,
		"threshold_credits", threshold.ThresholdCredits,
		"current_balance", currentBalance,
	)
	return nil
}
