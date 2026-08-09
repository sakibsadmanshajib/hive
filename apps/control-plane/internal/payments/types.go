package payments

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// IntentStatus is the state machine status for a payment intent.
type IntentStatus string

const (
	IntentStatusCreated             IntentStatus = "created"
	IntentStatusPendingRedirect     IntentStatus = "pending_redirect"
	IntentStatusProviderProcessing  IntentStatus = "provider_processing"
	IntentStatusConfirming          IntentStatus = "confirming"
	IntentStatusCompleted           IntentStatus = "completed"
	IntentStatusFailed              IntentStatus = "failed"
	IntentStatusExpired             IntentStatus = "expired"
	IntentStatusCancelled           IntentStatus = "cancelled"
)

// Rail identifies a payment rail provider.
type Rail string

const (
	RailStripe     Rail = "stripe"
	RailBkash      Rail = "bkash"
	RailSSLCommerz Rail = "sslcommerz"
)

// Monetary constants. All amounts are int64 micro-units.
const (
	// CreditsPerUSD: 1 USD = 100,000 Hive Credits.
	CreditsPerUSD int64 = 100_000

	// FXFeeRate is the markup applied to the mid-rate for BDT conversions.
	FXFeeRate = "0.05"

	// MinPurchaseCredits is the minimum credits purchasable in a single transaction.
	MinPurchaseCredits int64 = 1_000

	// MaxPurchaseCreditsStripe: 100 USD equiv (100 * 100,000 credits).
	MaxPurchaseCreditsStripe int64 = 10_000_000

	// MaxPurchaseCreditsSSLCommerz: based on BDT 500K limit.
	MaxPurchaseCreditsSSLCommerz int64 = 500_000_000

	// MaxPurchaseCreditsBkash: based on BDT 30K limit.
	MaxPurchaseCreditsBkash int64 = 30_000_000
)

// PredefinedTiers are the suggested credit purchase amounts.
var PredefinedTiers = []int64{1_000, 5_000, 10_000, 50_000, 100_000}

// Sentinel errors for the payments domain.
var (
	ErrInvalidTransition     = errors.New("payments: invalid status transition")
	ErrIntentNotFound        = errors.New("payments: payment intent not found")
	ErrBillingProfileRequired = errors.New("payments: billing profile required to initiate checkout")
	ErrFXUnavailable         = errors.New("payments: FX rate unavailable")

	// ErrEventRejected marks a webhook delivery the provider must NOT retry:
	// the payload failed signature verification, or is not a payload this rail
	// settles on. Redelivering the same bytes cannot change the outcome, so the
	// handler answers with a client error instead of asking for a retry. Every
	// other failure is treated as retryable (issue #628).
	ErrEventRejected = errors.New("payments: webhook event rejected")
)

// PaymentIntent is the core payment state machine record.
type PaymentIntent struct {
	ID               uuid.UUID      `json:"id"`
	AccountID        uuid.UUID      `json:"account_id"`
	Rail             Rail           `json:"rail"`
	Status           IntentStatus   `json:"status"`
	Credits          int64          `json:"credits"`
	// AmountUSD is internal accounting only — never serialised to customer
	// surface. Phase 17 FX/USD zero-leak (FX-17-01). Server→Stripe USD
	// payload (apps/control-plane/internal/payments/stripe/rail.go) reads
	// this field via the Go struct, NOT via JSON.
	AmountUSD        int64          `json:"-"`
	AmountLocal      int64          `json:"amount_local"`
	LocalCurrency    string         `json:"local_currency"`
	// FXSnapshotID — internal-only audit handle. PHASE-17-INTERNAL-ONLY:
	// PaymentIntent is the internal state record; customer-facing checkout/
	// invoice DTOs (FX-17-01..04) omit FX/USD entirely. `json:"-"` guarantees
	// the field never serialises onto a customer wire surface even if the
	// PaymentIntent struct itself is marshaled by mistake.
	FXSnapshotID     *uuid.UUID     `json:"-"`
	ProviderIntentID string         `json:"provider_intent_id"`
	RedirectURL      string         `json:"redirect_url"`
	TaxTreatment     string         `json:"tax_treatment"`
	TaxRate          string         `json:"tax_rate"`
	TaxAmountLocal   int64          `json:"tax_amount_local"`
	IdempotencyKey   string         `json:"idempotency_key"`
	ConfirmingAt     *time.Time     `json:"confirming_at,omitempty"`
	ExpiresAt        *time.Time     `json:"expires_at,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// PaymentEvent records a provider webhook event associated with an intent.
type PaymentEvent struct {
	ID               uuid.UUID       `json:"id"`
	PaymentIntentID  uuid.UUID       `json:"payment_intent_id"`
	EventType        string          `json:"event_type"`
	Rail             Rail            `json:"rail"`
	ProviderEventID  string          `json:"provider_event_id"`
	RawPayload       json.RawMessage `json:"raw_payload"`
	CreatedAt        time.Time       `json:"created_at"`
}

// DeliveryStatus is the lifecycle status of one inbound webhook delivery.
type DeliveryStatus string

const (
	// DeliveryStatusReceived is written before the payload is parsed, so a
	// failure at any later point is still recoverable and visible.
	DeliveryStatusReceived DeliveryStatus = "received"
	// DeliveryStatusProcessed marks a delivery whose settlement completed.
	DeliveryStatusProcessed DeliveryStatus = "processed"
	// DeliveryStatusFailed marks a delivery that could not be settled. These
	// rows are the settlement dead letter.
	DeliveryStatusFailed DeliveryStatus = "failed"
)

// WebhookDelivery is the durable record of one inbound provider webhook.
//
// It is written before anything is parsed or looked up, which is the whole
// point: settlement previously persisted nothing until the intent had been
// resolved, so a failure before that left no evidence the delivery ever arrived
// (issue #628). RawBody is stored verbatim as text because a dead letter must be
// able to hold a payload that is not valid JSON.
//
// Internal record only. It is never serialised onto a customer surface.
type WebhookDelivery struct {
	ID              uuid.UUID
	Rail            Rail
	PaymentIntentID *uuid.UUID
	Status          DeliveryStatus
	EventType       string
	ErrorDetail     string
	RawBody         string
	ReceivedAt      time.Time
}

// FXSnapshot records the FX rate used for a BDT transaction.
type FXSnapshot struct {
	ID            uuid.UUID `json:"id"`
	AccountID     uuid.UUID `json:"account_id"`
	BaseCurrency  string    `json:"base_currency"`
	QuoteCurrency string    `json:"quote_currency"`
	MidRate       string    `json:"mid_rate"`
	FeeRate       string    `json:"fee_rate"`
	EffectiveRate string    `json:"effective_rate"`
	SourceAPI     string    `json:"source_api"`
	FetchedAt     time.Time `json:"fetched_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// TaxResult holds the tax treatment decision for a checkout.
type TaxResult struct {
	TaxRate      string `json:"tax_rate"`
	TaxTreatment string `json:"tax_treatment"`
	TaxIncluded  bool   `json:"tax_included"`
	ReverseCharge bool  `json:"reverse_charge"`
}

// InitiateInput is passed to a PaymentRail to start a payment.
//
// PHASE-17-INTERNAL-ONLY: this struct is constructed server-side and passed
// in-process to rail implementations. It is never marshaled onto a customer
// HTTP response. The `AmountUSD` field stays in the struct because the
// Stripe rail consumes it directly, but the JSON tag is `-` so any future
// accidental marshal (debug endpoint, admin panel, audit log) cannot leak
// it on a customer wire.
type InitiateInput struct {
	PaymentIntentID uuid.UUID `json:"payment_intent_id"`
	AccountID       uuid.UUID `json:"account_id"`
	Credits         int64     `json:"credits"`
	// AmountUSD: PHASE-17-INTERNAL-ONLY — Stripe USD RPC reads this via the
	// Go struct, never JSON. `json:"-"` is defense-in-depth (FX-17 review).
	AmountUSD       int64     `json:"-"`
	AmountLocal     int64     `json:"amount_local"`
	Currency        string    `json:"currency"`
	// CallbackBaseURL is the control-plane origin a provider posts its
	// server-to-server webhook or IPN to. Settlement is driven from there and
	// nowhere else.
	CallbackBaseURL string `json:"callback_base_url"`
	// ReturnBaseURL is the console origin the customer's *browser* is sent back
	// to when the hosted payment page finishes. It is deliberately a separate
	// field from CallbackBaseURL: conflating the two is what landed paying
	// customers on a raw JSON webhook response (issue #538).
	ReturnBaseURL string `json:"return_base_url"`
	CustomerName  string `json:"customer_name"`
	CustomerEmail string `json:"customer_email"`
}

// InitiateResult is returned by a PaymentRail after initiating a payment.
type InitiateResult struct {
	ProviderIntentID string    `json:"provider_intent_id"`
	RedirectURL      string    `json:"redirect_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// RailEvent is the normalized event returned by a PaymentRail webhook processor.
type RailEvent struct {
	ProviderIntentID string `json:"provider_intent_id"`
	EventType        string `json:"event_type"`
	RawPayload       []byte `json:"raw_payload"`
}

// ValidatePurchaseAmount verifies credits are positive, a multiple of 1000, and
// no larger than the ceiling this rail already advertises through
// GetCheckoutOptions.
//
// The ceiling is enforced here rather than only in a client, because a caller
// that skips the console reaches InitiateCheckout directly. Credits is an int64
// on the wire but a float64 in every browser and in most JSON clients, so a
// quantity above 2^53 decodes cleanly into int64 while having already been
// rounded by the sender. Rejecting it keeps the advertised maximum and the
// enforced maximum the same number, and keeps a silently altered purchase
// quantity out of the ledger.
func ValidatePurchaseAmount(credits int64, rail Rail) error {
	if credits <= 0 {
		return fmt.Errorf("payments: credits must be positive, got %d", credits)
	}
	if credits%1000 != 0 {
		return fmt.Errorf("payments: credits must be a multiple of 1000, got %d", credits)
	}
	if maxCredits := maxCreditsForRail(rail); credits > maxCredits {
		return fmt.Errorf("payments: credits must be at most %d for the selected payment method, got %d", maxCredits, credits)
	}
	return nil
}

// AvailableRails returns the payment rails available for the given country code.
// BD gets all three rails; other countries get Stripe only.
func AvailableRails(countryCode string) []Rail {
	if countryCode == "BD" {
		return []Rail{RailStripe, RailBkash, RailSSLCommerz}
	}
	return []Rail{RailStripe}
}
