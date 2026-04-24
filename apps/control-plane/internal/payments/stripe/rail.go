package stripe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/paymentintent"
	"github.com/stripe/stripe-go/v84/webhook"

	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
)

// Rail implements the payments.PaymentRail interface for Stripe.
type Rail struct {
	secretKey     string
	webhookSecret string
}

// NewRail constructs a Stripe Rail and sets the global stripe API key.
func NewRail(secretKey, webhookSecret string) *Rail {
	stripego.Key = secretKey
	return &Rail{
		secretKey:     secretKey,
		webhookSecret: webhookSecret,
	}
}

// RailName returns the rail identifier for Stripe.
func (r *Rail) RailName() payments.Rail {
	return payments.RailStripe
}

// Initiate creates a Stripe PaymentIntent and returns the provider intent ID and redirect URL.
func (r *Rail) Initiate(ctx context.Context, input payments.InitiateInput) (payments.InitiateResult, error) {
	params := &stripego.PaymentIntentParams{
		Amount:   stripego.Int64(input.AmountUSD),
		Currency: stripego.String("usd"),
		AutomaticPaymentMethods: &stripego.PaymentIntentAutomaticPaymentMethodsParams{
			Enabled: stripego.Bool(true),
		},
		Metadata: map[string]string{
			"hive_payment_intent_id": input.PaymentIntentID.String(),
		},
	}
	params.IdempotencyKey = stripego.String(input.PaymentIntentID.String())

	pi, err := paymentintent.New(params)
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("stripe: create payment intent: %w", err)
	}

	redirectURL := input.CallbackBaseURL + "/checkout/stripe?payment_intent=" + pi.ID + "&client_secret=" + pi.ClientSecret
	if pi.NextAction != nil && pi.NextAction.RedirectToURL != nil && pi.NextAction.RedirectToURL.URL != "" {
		redirectURL = pi.NextAction.RedirectToURL.URL
	}

	return payments.InitiateResult{
		ProviderIntentID: pi.ID,
		RedirectURL:      redirectURL,
		ExpiresAt:        time.Now().Add(24 * time.Hour),
	}, nil
}

// ProcessEvent validates and parses a Stripe webhook payload into a normalized RailEvent.
func (r *Rail) ProcessEvent(_ context.Context, rawBody []byte, headers map[string]string) (payments.RailEvent, error) {
	// Case-insensitive lookup for Stripe-Signature header.
	var sig string
	for k, v := range headers {
		if strings.EqualFold(k, "stripe-signature") {
			sig = v
			break
		}
	}

	event, err := webhook.ConstructEventWithOptions(rawBody, sig, r.webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("stripe: webhook signature verification failed: %w", err)
	}

	var eventType string
	switch event.Type {
	case "payment_intent.succeeded":
		eventType = "payment.succeeded"
	case "payment_intent.payment_failed":
		eventType = "payment.failed"
	case "payment_intent.canceled":
		eventType = "payment.cancelled"
	default:
		return payments.RailEvent{}, fmt.Errorf("stripe: unsupported event type: %s", event.Type)
	}

	// Extract PaymentIntent ID from the event data object.
	var pi stripego.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		return payments.RailEvent{}, fmt.Errorf("stripe: unmarshal payment intent from event: %w", err)
	}

	return payments.RailEvent{
		ProviderIntentID: pi.ID,
		EventType:        eventType,
		RawPayload:       rawBody,
	}, nil
}
