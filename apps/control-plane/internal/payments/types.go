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
)

// PaymentIntent is the core payment state machine record.
type PaymentIntent struct {
	ID               uuid.UUID      `json:"id"`
	AccountID        uuid.UUID      `json:"account_id"`
	Rail             Rail           `json:"rail"`
	Status           IntentStatus   `json:"status"`
	Credits          int64          `json:"credits"`
	AmountUSD        int64          `json:"amount_usd"`
	AmountLocal      int64          `json:"amount_local"`
	LocalCurrency    string         `json:"local_currency"`
	FXSnapshotID     *uuid.UUID     `json:"fx_snapshot_id,omitempty"`
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
type InitiateInput struct {
	PaymentIntentID uuid.UUID `json:"payment_intent_id"`
	AccountID       uuid.UUID `json:"account_id"`
	Credits         int64     `json:"credits"`
	AmountUSD       int64     `json:"amount_usd"`
	AmountLocal     int64     `json:"amount_local"`
	Currency        string    `json:"currency"`
	CallbackBaseURL string    `json:"callback_base_url"`
	CustomerName    string    `json:"customer_name"`
	CustomerEmail   string    `json:"customer_email"`
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

// ValidatePurchaseAmount verifies credits are positive and a multiple of 1000.
func ValidatePurchaseAmount(credits int64) error {
	if credits <= 0 {
		return fmt.Errorf("payments: credits must be positive, got %d", credits)
	}
	if credits%1000 != 0 {
		return fmt.Errorf("payments: credits must be a multiple of 1000, got %d", credits)
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
