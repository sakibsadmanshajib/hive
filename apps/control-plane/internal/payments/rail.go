package payments

import "context"

// PaymentRail is the interface that every payment provider must implement.
// It handles the provider-specific initiation and webhook event processing.
type PaymentRail interface {
	// RailName returns the rail identifier for this implementation.
	RailName() Rail

	// Initiate creates a payment with the provider and returns a redirect URL.
	Initiate(ctx context.Context, input InitiateInput) (InitiateResult, error)

	// ProcessEvent parses and validates a provider webhook payload.
	ProcessEvent(ctx context.Context, rawBody []byte, headers map[string]string) (RailEvent, error)
}
