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

	// FXFeeRate is the markup folded into the mid-rate for BDT conversions,
	// 2.5 percent (D-066, owner ruling 2026-09-02, down from 5 percent).
	//
	// It is folded INTO the rate and is never a line item. A BD customer sees
	// one rate and one local price: if the mid rate is 127.00 the rate used and
	// shown is 127.00 x 1.025 = 130.175. Do not add a fee row to any
	// customer-facing surface, and do not present this figure as a separate
	// charge.
	//
	// fx.go derives its multiplier from this string rather than restating it,
	// so the fee_rate stored on an fx_snapshots row is always the fee that was
	// actually applied to the effective_rate on the same row. Before that it
	// was a hardcoded 105/100 beside a stored "0.05", which is the shape issue
	// #1682 was filed about: a stored money figure that cannot be reproduced
	// from its own row.
	FXFeeRate = "0.025"

	// PurchaseMarkupRate is the markup on the PRICE of credits, 6 percent
	// (D-065, owner ruling 2026-09-02). One billion credits (one USD of credit
	// value at the D-046 peg) costs 1.06 USD.
	//
	// The peg itself is untouched: a purchase still GRANTS credits at
	// CreditsPerUSD, and this rate only decides what the buyer pays for them.
	// It is where the product takes its margin now that D-064 retired the 1.4
	// multiplier from the inference path, so it must never be applied twice,
	// and never on the burn side.
	PurchaseMarkupRate = "0.06"

	// CreditIncrement is the GRANULARITY of a purchase, one USD cent, not its
	// smallest permitted size: ValidatePurchaseAmount rejects a quantity that
	// is not a whole multiple of it, and separately rejects anything under
	// MinPurchaseCredits. One cent has not been buyable since issue #1450.
	CreditIncrement int64 = CreditsPerUSD / 100

	// StripeMinimumChargeCredits is Stripe's own minimum charge for USD, 0.50
	// (https://docs.stripe.com/currencies), expressed in credits. Stripe is the
	// default and only non-BD rail, so a purchase under this is one the rail
	// would refuse whatever this product thinks of it. Named here rather than
	// left in prose because it is one of the two reasons the floor below is the
	// size it is, and purchase_floor_test.go asserts it.
	StripeMinimumChargeCredits int64 = CreditsPerUSD / 2

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
	// smaller radius: it funds a single request in flight and refuses the
	// second concurrent one. Ten is not ten sequential turns, which would be a
	// much larger number, since a hold is released at settlement and issue
	// #1450's own live evidence has a real turn settling under one cent. It is
	// ten CONCURRENT flat holds.
	//
	// The flat figure is not what binds on the product's default alias, and
	// that is the reason ten rather than two. ChatHoldCredits is the endpoint
	// FLOOR that edge-api's ReservationCredits raises per request for a
	// variable-price alias such as hive-auto, sized from that request's own
	// bounds (issue #1372). An ordinary hive-auto turn, one that sets no
	// max_tokens and so holds against the full completion cap, sizes to about
	// $0.34. Ten flat holds covers roughly three of those; two would not cover
	// one.
	//
	// It also puts the smallest purchase at twice StripeMinimumChargeCredits.
	// The previous one-cent minimum was below it, so the smallest quantity the
	// product offered was one the default rail would have rejected outright.
	//
	// What the floor deliberately does NOT cover, stated precisely because the
	// imprecise version of this list was wrong in a way that mattered:
	//
	//  1. A dispatched body past roughly 152 KiB, whose sized hold exceeds
	//     $1.00. On /v1/chat/completions that means a 152 KiB prompt, which is
	//     not an ordinary message. On /v1/rag/chat it does NOT: the body the
	//     hold is sized from is the customer's messages PLUS the retrieved
	//     grounding block (rag/chat_handler.go), so a one-line question with a
	//     large top_k reaches the same size. top_k is a request field capped at
	//     100, and chunks target about 2 KiB, so a top_k in the high seventies
	//     puts the grounding block alone over the line. That is issue #1450
	//     again on a surface this constant cannot fix, because the fix there is
	//     to bound the grounding block or size the hold from the customer's
	//     half, not to raise what people must pay.
	//  2. The $2.00 catalog envelope ReservationCredits falls back to when it
	//     cannot size a request at all, which is a body over the 256 KiB cap
	//     that is refused a moment later anyway, or a pricing fault.
	//
	// Covering either by purchase floor rather than by sizing the hold would
	// price the entry point of a Bangladesh-first prepaid product at $2.00 to
	// close cases a payer cannot reach with an ordinary message. The hold being
	// unrelated to the charge is the root defect and stays with issues #699 and
	// #1372; this constant is not a substitute for it.
	//
	// What the guards actually enforce is a multiple of 7, not 10: $0.70 clears
	// both StripeMinimumChargeCredits and two smallest variable-price holds.
	// Ten is the chosen value and $0.30 of that is deliberate headroom above
	// the enforced minimum, so lowering it to 8 or 9 is a product decision the
	// tests will allow and lowering it to 6 is one they will stop. Said plainly
	// here because claiming the tests pin 10 when they pin 7 is the same kind
	// of overstatement this change exists to remove.
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

	// ErrBelowMinimumPurchase marks a purchase quantity under
	// MinPurchaseCredits. It is a sentinel rather than a message the HTTP layer
	// re-recognises by substring, because the neighbouring errors interpolate
	// caller-supplied values: `rail %s not available for country %s` carries an
	// unvalidated rail straight into the string, so a rail chosen to contain
	// another branch's phrase can pick which canned 400 the payer gets. Nothing
	// leaks and nothing is bypassed either way, but a new branch should not
	// widen a set that is matched on text.
	ErrBelowMinimumPurchase = errors.New("payments: purchase below the minimum")

	// ErrRailNotAvailable marks a rail the account's country cannot select.
	//
	// A sentinel for the same reason as the one above, and it is the error that
	// made the reason concrete: its message interpolates BOTH the caller's raw
	// rail string and the account's country code, neither of which is
	// constrained to a known set. A rail or a country containing another
	// branch's phrase could therefore pick which canned 400 the payer got.
	// Nothing leaked and nothing was bypassed, since every branch returns a
	// fixed string and all of them answer 400, but text is not an identity and
	// should not be used as one on this path.
	ErrRailNotAvailable = errors.New("payments: rail not available for this account")

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
		return fmt.Errorf("%w: credits must be at least %d, got %d", ErrBelowMinimumPurchase, MinPurchaseCredits, credits)
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
