package batches

import (
	"context"
	"fmt"

	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
)

// AccountingAdapter adapts *inference.AccountingClient to the batches.AccountingBackend interface.
type AccountingAdapter struct {
	inner *inference.AccountingClient
}

// NewAccountingAdapter wraps an AccountingClient for use with the batches Handler.
func NewAccountingAdapter(inner *inference.AccountingClient) *AccountingAdapter {
	return &AccountingAdapter{inner: inner}
}

// CreateReservation reserves credits for a batch job.
func (a *AccountingAdapter) CreateReservation(ctx context.Context, input ReservationInput) (string, error) {
	result, err := a.inner.CreateReservation(ctx, inference.CreateReservationInput{
		AccountID:        input.AccountID,
		RequestID:        input.RequestID,
		AttemptNumber:    1,
		APIKeyID:         input.APIKeyID,
		Endpoint:         input.Endpoint,
		ModelAlias:       input.ModelAlias,
		EstimatedCredits: input.EstimatedCredits,
		PolicyMode:       "strict",
	})
	if err != nil {
		return "", fmt.Errorf("batches: create reservation: %w", err)
	}
	return result.ID, nil
}
