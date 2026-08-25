package stripe_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	stripego "github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/webhook"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	stripeRail "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/stripe"
)

// buildSignedPayload creates a signed Stripe webhook payload whose data object is
// a PaymentIntent. Checkout Session events are built by signedSessionEvent in
// return_test.go; this helper exists to prove that PaymentIntent-shaped events
// are not a settlement path for this rail.
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
		Object     string              `json:"object"`
		Type       stripego.EventType  `json:"type"`
		APIVersion string              `json:"api_version"`
		Data       *stripego.EventData `json:"data"`
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

// stripeRail satisfying payments.PaymentRail is a compile-time
// property, not a runtime behaviour: a test function whose body is only
// `var _ Interface = X` passes unconditionally regardless of what NewRail
// does, so it asserted nothing that go build wasn't already checking every
// time this package compiles. Declared here at package scope as a type-only
// assertion on a nil *Rail, where the compiler still enforces it, it no
// longer counts as a passing test, and package initialization no longer
// calls NewRail (which would mutate the global stripego.Key before any
// test runs and could leak that state into later tests).
var _ payments.PaymentRail = (*stripeRail.Rail)(nil)

// TestStripeProcessEvent_PaymentIntentEventsAreNotASettlementPath documents the
// rail contract after the move to hosted Checkout Sessions: the session is the
// unit of payment, and a bare PaymentIntent event carries an id that cannot be
// resolved back to a Hive intent, so it must not settle anything.
func TestStripeProcessEvent_PaymentIntentEventsAreNotASettlementPath(t *testing.T) {
	const webhookSecret = "whsec_testvalidwebhooksecret12345"

	for _, eventType := range []string{
		"payment_intent.succeeded",
		"payment_intent.payment_failed",
		"payment_intent.canceled",
	} {
		t.Run(eventType, func(t *testing.T) {
			rail := stripeRail.NewRail("sk_test_key", webhookSecret)
			rawBody, sig := buildSignedPayload(t, eventType, "pi_test_1234", webhookSecret)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := rail.ProcessEvent(ctx, rawBody, map[string]string{
				"Stripe-Signature": sig,
			})
			if err == nil {
				t.Fatalf("expected %s to be rejected, got no error", eventType)
			}
			// Loud, not quiet: an operator reading the log must be told the
			// endpoint subscription is wrong, otherwise a prepaid customer pays
			// and is never credited.
			for _, want := range []string{"MISCONFIGURED WEBHOOK ENDPOINT", "checkout.session.completed"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected the error to mention %q, got: %v", want, err)
				}
			}
		})
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
