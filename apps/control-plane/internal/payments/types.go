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
	IntentStatusCreated            IntentStatus = "created"
	IntentStatusPendingRedirect    IntentStatus = "pending_redirect"
	IntentStatusProviderProcessing IntentStatus = "provider_processing"
	IntentStatusConfirming         IntentStatus = "confirming"
	IntentStatusCompleted          IntentStatus = "completed"
	IntentStatusFailed             IntentStatus = "failed"
	IntentStatusExpired            IntentStatus = "expired"
	IntentStatusCancelled          IntentStatus = "cancelled"
)

// Rail identifies a payment rail provider.
type Rail string

const (
	RailStripe     Rail = "stripe"
	RailBkash      Rail = "bkash"
	RailSSLCommerz Rail = "sslcommerz"
)

// Monetary constants. All credit figures are int64 and denominated in the
// CURRENT credit unit: 1 USD = 1,000,000,000 Hive Credits (owner directive,
// 2026-08-23; rescaled from the historical 1 USD = 100,000 by a factor of
// 10,000). Stored balances and history were rescaled to match by migration
// 20260823_40_credit_unit_rescale_billion.sql, so every figure in the ledger,
// the catalog and this file speaks the same unit. A credit is now one
// billionth of a USD-equivalent.
const (
	// CreditsPerUSD: 1 USD = 1,000,000,000 Hive Credits.
	CreditsPerUSD int64 = 1_000_000_000

	// FXFeeRate is the markup applied to the mid-rate for BDT conversions.
	FXFeeRate = "0.05"

	// CreditIncrement is the smallest purchasable credit step: one USD cent
	// (ValidatePurchaseAmount rejects quantities that are not a whole
	// multiple of it).
	CreditIncrement int64 = CreditsPerUSD / 100

	// ChatHoldCredits mirrors the authorization hold the data plane takes
	// before it dispatches a chat request: edge-api's
	// inference.DefaultHoldText, $0.10 equivalent. It is a hold and never a
	// charge, and settlement replaces it with the catalog price of what was
	// actually metered, but it is the amount a buyer must be holding before
	// their first message is allowed through at all.
	//
	// It is mirrored rather than imported because the hold is declared in
	// apps/edge-api and this is apps/control-plane: separate Go modules with
	// no dependency edge, and neither plane should acquire one to share a
	// literal. purchase_floor_test.go reads the real declaration out of
	// edge-api's source and fails if this mirror drifts from it, in either
	// direction.
	ChatHoldCredits int64 = CreditsPerUSD / 10 // 100,000,000

	// MinPurchaseHoldMultiple is how many chat holds the smallest purchase
	// must cover (issue #1450).
	//
	// Ten, not one. A minimum of exactly one hold is the same defect with a
	// smaller radius: it funds a single message in flight and refuses the
	// second concurrent one, and it does not cover a variable-price alias at
	// all, because ChatHoldCredits is the endpoint FLOOR that edge-api's
	// ReservationCredits raises per request for hive-auto from that request's
	// own bounds (issue #1372). Ten holds is a session rather than a message:
	// roughly ten ordinary turns of headroom, or at least two of the larger
	// variable-price holds. It also puts the smallest purchase above Stripe's
	// own $0.50 USD minimum charge, which the previous one-cent minimum was
	// below, so the smallest quantity the product offered was one the default
	// rail would have rejected outright.
	MinPurchaseHoldMultiple int64 = 10

	// MinPurchaseCredits is the minimum credits purchasable in a single
	// transaction, derived from the hold it has to clear rather than picked
	// independently of it: $1.00 equivalent.
	//
	// Before issue #1450 this was one cent, one tenth of a single hold, so a
	// customer who bought the minimum was refused with "Your available credit
	// does not cover this request" on their first message. The two constants
	// were each individually correct and had simply never been compared.
	MinPurchaseCredits int64 = ChatHoldCredits * MinPurchaseHoldMultiple // 1,000,000,000

	// MaxPurchaseCreditsStripe: 100 USD equiv (100 * CreditsPerUSD).
	MaxPurchaseCreditsStripe int64 = 100 * CreditsPerUSD

	// MaxPurchaseCreditsSSLCommerz: based on BDT 500K limit.
	MaxPurchaseCreditsSSLCommerz int64 = 5_000_000_000_000

	// MaxPurchaseCreditsBkash: based on BDT 30K limit.
	MaxPurchaseCreditsBkash int64 = 300_000_000_000
)

// PredefinedTiers are the suggested credit purchase amounts: $1.00, $2.00,
// $5.00, $10.00 and $20.00 equivalents.
//
// Multiples of MinPurchaseCredits rather than five independent literals, so no
// suggestion can sit below the floor by construction. Two of the five used to:
// the $0.01 and $0.05 tiers could not cover one chat hold, and the $0.10 tier
// covered exactly one with nothing left for a second concurrent message
// (issue #1450). A tier below the floor is worse than a permissive floor,
// because the product is actively proposing the amount that will not work.
var PredefinedTiers = []int64{
	1 * MinPurchaseCredits,  // $1.00
	2 * MinPurchaseCredits,  // $2.00
	5 * MinPurchaseCredits,  // $5.00
	10 * MinPurchaseCredits, // $10.00
	20 * MinPurchaseCredits, // $20.00
}

// Sentinel errors for the payments domain.
var (
	ErrInvalidTransition      = errors.New("payments: invalid status transition")
	ErrIntentNotFound         = errors.New("payments: payment intent not found")
	ErrBillingProfileRequired = errors.New("payments: billing profile required to initiate checkout")
	ErrFXUnavailable          = errors.New("payments: FX rate unavailable")

	// ErrEventRejected marks a webhook delivery the provider must NOT retry:
	// the payload failed signature verification, or is not a payload this rail
	// settles on. Redelivering the same bytes cannot change the outcome, so the
	// handler answers with a client error instead of asking for a retry. Every
	// other failure is treated as retryable (issue #628).
	ErrEventRejected = errors.New("payments: webhook event rejected")
)

// PaymentIntent is the core payment state machine record.
type PaymentIntent struct {
	ID        uuid.UUID    `json:"id"`
	AccountID uuid.UUID    `json:"account_id"`
	Rail      Rail         `json:"rail"`
	Status    IntentStatus `json:"status"`
	Credits   int64        `json:"credits"`
	// AmountUSD is internal accounting only — never serialised to customer
	// surface. Phase 17 FX/USD zero-leak (FX-17-01). Server→Stripe USD
	// payload (apps/control-plane/internal/payments/stripe/rail.go) reads
	// this field via the Go struct, NOT via JSON.
	AmountUSD     int64  `json:"-"`
	AmountLocal   int64  `json:"amount_local"`
	LocalCurrency string `json:"local_currency"`
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
	ID              uuid.UUID       `json:"id"`
	PaymentIntentID uuid.UUID       `json:"payment_intent_id"`
	EventType       string          `json:"event_type"`
	Rail            Rail            `json:"rail"`
	ProviderEventID string          `json:"provider_event_id"`
	RawPayload      json.RawMessage `json:"raw_payload"`
	CreatedAt       time.Time       `json:"created_at"`
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
	TaxRate       string `json:"tax_rate"`
	TaxTreatment  string `json:"tax_treatment"`
	TaxIncluded   bool   `json:"tax_included"`
	ReverseCharge bool   `json:"reverse_charge"`
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
	AmountUSD   int64  `json:"-"`
	AmountLocal int64  `json:"amount_local"`
	Currency    string `json:"currency"`
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

// ValidatePurchaseAmount verifies credits are positive, a whole number of
// one-cent steps (CreditIncrement), at least the floor and no larger than the
// ceiling this rail already advertises through GetCheckoutOptions.
//
// The floor is enforced here for the same reason the ceiling is, and it was
// missing until issue #1450: MinPurchaseCredits was published as min_credits
// and clamped only by the console, so a caller reaching InitiateCheckout
// directly could still buy an amount the first request would refuse to spend.
// An advertised minimum and an enforced minimum that are allowed to differ are
// the same two-uncoupled-numbers defect one layer up from the one this floor
// exists to fix.
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
	if credits%CreditIncrement != 0 {
		return fmt.Errorf("payments: credits must be a multiple of %d, got %d", CreditIncrement, credits)
	}
	if credits < MinPurchaseCredits {
		return fmt.Errorf("payments: credits must be at least %d, got %d", MinPurchaseCredits, credits)
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
