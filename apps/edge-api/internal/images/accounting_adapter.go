package images

import (
	"context"
	"fmt"

	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
)

// AccountingAdapter adapts *inference.AccountingClient to the images.AccountingInterface.
type AccountingAdapter struct {
	inner *inference.AccountingClient
}

// NewAccountingAdapter wraps an AccountingClient for use with the images Handler.
func NewAccountingAdapter(inner *inference.AccountingClient) *AccountingAdapter {
	return &AccountingAdapter{inner: inner}
}

// CreateReservation reserves credits for an image generation or edit request.
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
		return "", fmt.Errorf("images: create reservation: %w", err)
	}
	return result.ID, nil
}

// FinalizeReservation marks a reservation as completed with actual credit usage.
func (a *AccountingAdapter) FinalizeReservation(ctx context.Context, input FinalizeInput) error {
	return a.inner.FinalizeReservation(ctx, inference.FinalizeReservationInput{
		AccountID:              input.AccountID,
		ReservationID:          input.ReservationID,
		ActualCredits:          input.ActualCredits,
		TerminalUsageConfirmed: true,
		Status:                 "completed",
	})
}

// ReleaseReservation releases a reservation without charging credits.
func (a *AccountingAdapter) ReleaseReservation(ctx context.Context, accountID, reservationID, reason string) error {
	return a.inner.ReleaseReservation(ctx, inference.ReleaseReservationInput{
		AccountID:     accountID,
		ReservationID: reservationID,
		Reason:        reason,
	})
}
