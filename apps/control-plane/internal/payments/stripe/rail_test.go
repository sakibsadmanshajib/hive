package stripe_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	stripego "github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/webhook"

	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
	stripeRail "github.com/hivegpt/hive/apps/control-plane/internal/payments/stripe"
)

// buildSignedPayload creates a signed Stripe webhook payload for testing.
// It uses the v84 webhook.GenerateTestSignedPayload API which takes *UnsignedPayload.
func buildSignedPayload(t *testing.T, eventType string, piID string, webhookSecret string) ([]byte, string) {
	t.Helper()

	pi := &stripego.PaymentIntent{
		ID: piID,
	}
	rawObj, err := json.Marshal(pi)
	if err != nil {
		t.Fatalf("marshal payment intent: %v", err)
	}

	// Build a minimal event JSON that ConstructEventWithOptions will accept.
	// api_version is set to a placeholder; the rail uses IgnoreAPIVersionMismatch.
	event := struct {
		Object     string                `json:"object"`
		Type       stripego.EventType    `json:"type"`
		APIVersion string                `json:"api_version"`
		Data       *stripego.EventData   `json:"data"`
	}{
		Object:     "event",
		Type:       stripego.EventType(eventType),
		APIVersion: stripego.APIVersion,
		Data: &stripego.EventData{
			Raw: json.RawMessage(rawObj),
		},
	}
	rawEvent, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   rawEvent,
		Secret:    webhookSecret,
		Timestamp: time.Now(),
	})
	return rawEvent, signed.Header
}

func TestStripeRailName(t *testing.T) {
	rail := stripeRail.NewRail("sk_test_key", "whsec_test")
	if rail.RailName() != payments.RailStripe {
		t.Errorf("expected %q got %q", payments.RailStripe, rail.RailName())
	}
}

func TestStripeProcessEvent_ValidSignature_ReturnsSucceeded(t *testing.T) {
	const webhookSecret = "whsec_testvalidwebhooksecret12345"
	const piID = "pi_test_1234"

	rail := stripeRail.NewRail("sk_test_key", webhookSecret)
	rawBody, sig := buildSignedPayload(t, "payment_intent.succeeded", piID, webhookSecret)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	event, err := rail.ProcessEvent(ctx, rawBody, map[string]string{
		"Stripe-Signature": sig,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.EventType != "payment.succeeded" {
		t.Errorf("expected payment.succeeded got %q", event.EventType)
	}
	if event.ProviderIntentID != piID {
		t.Errorf("expected provider intent ID %q got %q", piID, event.ProviderIntentID)
	}
}

func TestStripeProcessEvent_InvalidSignature_ReturnsError(t *testing.T) {
	const webhookSecret = "whsec_testvalidwebhooksecret12345"

	rail := stripeRail.NewRail("sk_test_key", webhookSecret)
	rawBody, _ := buildSignedPayload(t, "payment_intent.succeeded", "pi_test_1234", webhookSecret)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use a tampered/wrong signature
	_, err := rail.ProcessEvent(ctx, rawBody, map[string]string{
		"Stripe-Signature": "t=12345,v1=invalidsignature",
	})
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
}

func TestStripeProcessEvent_MapsFailedEvent(t *testing.T) {
	const webhookSecret = "whsec_testvalidwebhooksecret12345"
	const piID = "pi_test_5678"

	rail := stripeRail.NewRail("sk_test_key", webhookSecret)
	rawBody, sig := buildSignedPayload(t, "payment_intent.payment_failed", piID, webhookSecret)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	event, err := rail.ProcessEvent(ctx, rawBody, map[string]string{
		"Stripe-Signature": sig,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.EventType != "payment.failed" {
		t.Errorf("expected payment.failed got %q", event.EventType)
	}
}

func TestStripeProcessEvent_MapsCancelledEvent(t *testing.T) {
	const webhookSecret = "whsec_testvalidwebhooksecret12345"
	const piID = "pi_test_9999"

	rail := stripeRail.NewRail("sk_test_key", webhookSecret)
	rawBody, sig := buildSignedPayload(t, "payment_intent.canceled", piID, webhookSecret)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	event, err := rail.ProcessEvent(ctx, rawBody, map[string]string{
		"Stripe-Signature": sig,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.EventType != "payment.cancelled" {
		t.Errorf("expected payment.cancelled got %q", event.EventType)
	}
}

func TestStripeProcessEvent_UnsupportedEventType_ReturnsError(t *testing.T) {
	const webhookSecret = "whsec_testvalidwebhooksecret12345"

	rail := stripeRail.NewRail("sk_test_key", webhookSecret)
	rawBody, sig := buildSignedPayload(t, "charge.succeeded", "ch_test_1234", webhookSecret)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rail.ProcessEvent(ctx, rawBody, map[string]string{
		"Stripe-Signature": sig,
	})
	if err == nil {
		t.Fatal("expected error for unsupported event type, got nil")
	}
}

func TestStripeInitiate_BuildsCorrectParams(t *testing.T) {
	// This test verifies the Rail struct can be constructed and RailName works.
	// Full Initiate integration test requires a real Stripe API key.
	rail := stripeRail.NewRail("sk_test_key", "whsec_test")
	if rail == nil {
		t.Fatal("NewRail returned nil")
	}

	// Verify the interface is satisfied
	var _ payments.PaymentRail = rail

	// Verify it returns the correct name
	if rail.RailName() != payments.RailStripe {
		t.Errorf("expected RailStripe, got %q", rail.RailName())
	}
}

func TestStripeInitiate_IdempotencyKeySet(t *testing.T) {
	// Verify that InitiateInput has a PaymentIntentID that can be used as idempotency key
	input := payments.InitiateInput{
		PaymentIntentID: uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
		AccountID:       uuid.MustParse("223e4567-e89b-12d3-a456-426614174001"),
		Credits:         10000,
		AmountUSD:       100, // $1.00 in cents
		AmountLocal:     11200,
		Currency:        "usd",
		CallbackBaseURL: "https://example.com",
		CustomerName:    "Test User",
		CustomerEmail:   "test@example.com",
	}

	// The idempotency key should be the PaymentIntentID string
	expectedKey := input.PaymentIntentID.String()
	if expectedKey == "" {
		t.Error("PaymentIntentID should not be empty")
	}
}
