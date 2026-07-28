package stripe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/checkout/session"
	"github.com/stripe/stripe-go/v84/webhook"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
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

// Initiate creates a Stripe-hosted Checkout Session and returns its id plus the
// hosted page URL to send the customer to.
//
// A hosted Checkout Session, rather than a bare PaymentIntent, is what makes
// this rail structurally identical to SSLCommerz: the provider hosts the payment
// form, the provider returns the browser to a URL we choose, and a webhook
// settles. The previous implementation created a bare PaymentIntent and then
// pointed the customer at `${controlPlane}/checkout/stripe`, a route that
// existed in no service, with the client secret pasted into the query string
// (issue #538).
func (r *Rail) Initiate(_ context.Context, input payments.InitiateInput) (payments.InitiateResult, error) {
	// Refuse before contacting Stripe: a checkout with no usable console origin
	// would take the customer's money and then strand their browser.
	if err := payments.ValidateReturnBaseURL(input.ReturnBaseURL); err != nil {
		return payments.InitiateResult{}, fmt.Errorf("stripe: %w", err)
	}

	// No outcome is encoded in success_url. The return page reads the
	// authoritative state from the payment intent record, so a customer editing
	// the URL changes nothing. cancel_url carries a copy hint only.
	successURL := payments.BrowserReturnURL(input.ReturnBaseURL, payments.CheckoutReturnPath, input.PaymentIntentID, payments.RailStripe, "")
	cancelURL := payments.BrowserReturnURL(input.ReturnBaseURL, payments.CheckoutReturnPath, input.PaymentIntentID, payments.RailStripe, payments.ReturnHintCancelled)

	params := &stripego.CheckoutSessionParams{
		Mode:              stripego.String(string(stripego.CheckoutSessionModePayment)),
		SuccessURL:        stripego.String(successURL),
		CancelURL:         stripego.String(cancelURL),
		ClientReferenceID: stripego.String(input.PaymentIntentID.String()),
		LineItems: []*stripego.CheckoutSessionLineItemParams{{
			Quantity: stripego.Int64(1),
			PriceData: &stripego.CheckoutSessionLineItemPriceDataParams{
				Currency:   stripego.String("usd"),
				UnitAmount: stripego.Int64(input.AmountUSD),
				ProductData: &stripego.CheckoutSessionLineItemPriceDataProductDataParams{
					Name: stripego.String("Hive Credits"),
				},
			},
		}},
		Metadata: map[string]string{
			"hive_payment_intent_id": input.PaymentIntentID.String(),
		},
	}
	params.IdempotencyKey = stripego.String(input.PaymentIntentID.String())

	sess, err := session.New(params)
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("stripe: create checkout session: %w", err)
	}
	if sess.URL == "" {
		return payments.InitiateResult{}, fmt.Errorf("stripe: checkout session has no hosted URL")
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	if sess.ExpiresAt > 0 {
		expiresAt = time.Unix(sess.ExpiresAt, 0).UTC()
	}

	return payments.InitiateResult{
		// The session id is the provider intent id for this rail: it is known at
		// initiation time and it is what the checkout.session.* webhooks carry,
		// so the webhook can always resolve back to this Hive intent.
		ProviderIntentID: sess.ID,
		RedirectURL:      sess.URL,
		ExpiresAt:        expiresAt,
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

	// The Checkout Session is this rail's unit of payment, so only
	// checkout.session.* events are settled. The PaymentIntent that Stripe
	// creates inside a session is not known at initiation time, so
	// payment_intent.* events carry an id that cannot be resolved back to a Hive
	// intent and are deliberately not handled.
	var sess stripego.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		return payments.RailEvent{}, fmt.Errorf("stripe: unmarshal checkout session from event: %w", err)
	}
	if sess.ID == "" {
		return payments.RailEvent{}, fmt.Errorf("stripe: event %s carries no checkout session id", event.Type)
	}

	var eventType string
	switch event.Type {
	case "checkout.session.completed":
		// A completed session is only money in the bank once Stripe says it is
		// paid. Delayed payment methods complete the session first and settle
		// later through async_payment_succeeded.
		if sess.PaymentStatus != stripego.CheckoutSessionPaymentStatusPaid {
			return payments.RailEvent{}, fmt.Errorf(
				"stripe: checkout session %s completed with payment_status %q; awaiting settlement",
				sess.ID, sess.PaymentStatus)
		}
		eventType = "payment.succeeded"
	case "checkout.session.async_payment_succeeded":
		eventType = "payment.succeeded"
	case "checkout.session.async_payment_failed":
		eventType = "payment.failed"
	case "checkout.session.expired":
		eventType = "payment.expired"
	default:
		return payments.RailEvent{}, fmt.Errorf("stripe: unsupported event type: %s", event.Type)
	}

	return payments.RailEvent{
		ProviderIntentID: sess.ID,
		EventType:        eventType,
		RawPayload:       rawBody,
	}, nil
}
