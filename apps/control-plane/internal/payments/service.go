package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// ---------------------------------------------------------------------------
// Dependency interfaces (accept interfaces, return structs)
// ---------------------------------------------------------------------------

// LedgerGranter posts credit grant entries to the ledger.
type LedgerGranter interface {
	GrantCredits(ctx context.Context, accountID uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error)
}

// ProfileReader reads the profile facts the payment path needs.
//
// Country arrives as a bare code rather than as a whole AccountProfile on
// purpose. Both callers below want only the country, to choose a rail set, and
// reading the whole profile for it made a missing `account_profiles` row fatal
// on the entire checkout surface (issue #1386). Narrowing the port removes the
// opportunity: there is now no way to ask this question from here that can fail
// on an absent row. profiles.Service.CountryCode owns that mapping.
type ProfileReader interface {
	GetBillingProfile(ctx context.Context, accountID uuid.UUID) (profiles.BillingProfile, error)
	CountryCode(ctx context.Context, accountID uuid.UUID) (string, error)
}

// FXProvider creates FX snapshots for BDT transactions.
type FXProvider interface {
	CreateSnapshot(ctx context.Context, repo Repository, accountID uuid.UUID) (FXSnapshot, error)
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

// Service orchestrates the payment intent lifecycle.
type Service struct {
	repo     Repository
	ledger   LedgerGranter
	profiles ProfileReader
	fx       FXProvider
	rails    map[Rail]PaymentRail
}

// NewService constructs a Service with all dependencies injected.
func NewService(repo Repository, ledgerSvc LedgerGranter, profilesSvc ProfileReader, fxSvc FXProvider, rails map[Rail]PaymentRail) *Service {
	return &Service{
		repo:     repo,
		ledger:   ledgerSvc,
		profiles: profilesSvc,
		fx:       fxSvc,
		rails:    rails,
	}
}

// ---------------------------------------------------------------------------
// InitiateCheckout
// ---------------------------------------------------------------------------

// InitiateCheckout creates a payment intent and returns a redirect URL.
//
// Flow:
//  1. Validate credits amount
//  2. Verify rail is available for the account's country, then that this
//     deployment holds its credentials
//  3. Load and validate billing profile (gate)
//  4. Calculate tax treatment
//  5. Compute amounts (USD cents, local paisa for BD)
//  6. Create FX snapshot for BD rails
//  7. Insert payment intent
//  8. Call rail.Initiate
//  9. Update provider details
//  10. Transition created -> pending_redirect
//
// callbackBaseURL is the control-plane origin providers post webhooks to.
// returnBaseURL is the console origin the customer's browser is returned to.
// The two are never the same thing (issue #538).
func (s *Service) InitiateCheckout(ctx context.Context, accountID uuid.UUID, rail Rail, credits int64, callbackBaseURL, returnBaseURL, idempotencyKey string) (*PaymentIntent, error) {
	// 1. Validate credits.
	if err := ValidatePurchaseAmount(credits, rail); err != nil {
		return nil, err
	}

	// A checkout with no usable browser return origin would strand the payer
	// after the money moved, so refuse it before anything is created.
	if err := ValidateReturnBaseURL(returnBaseURL); err != nil {
		return nil, err
	}

	// 2. Verify rail availability for the account's country. An account whose
	// country cannot be resolved gets AvailableRails(""), the non-BD set, so an
	// unknown country can only ever keep rail access at the default and never
	// widen it to a BD rail.
	countryCode, err := s.profiles.CountryCode(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("payments: get account country: %w", err)
	}
	available := AvailableRails(countryCode)
	if !railIn(rail, available) {
		// %q, not %s: both values are caller-influenced and neither is
		// constrained to a known set, so an unquoted one can forge a line in the
		// log this design treats as the trustworthy half of the error split.
		return nil, fmt.Errorf("%w: rail %q not available for country %q", ErrRailNotAvailable, rail, countryCode)
	}

	// Only now, once the caller is entitled to this rail at all, ask whether the
	// deployment holds credentials for it. A rail is registered from its whole
	// credential set at startup, so an absent one means this box can neither take
	// the payment nor settle it. Asking last, which is what this used to do,
	// meant a billing profile read, an FX snapshot and an inserted payment intent
	// all happened first, leaving a stranded `created` intent behind on every
	// attempt (issue #1449). Asking first, which the first cut of that fix did,
	// answered a rail the caller was never entitled to select with a 503 rather
	// than a 400, blaming the deployment for customer input and telling an
	// authenticated caller which rails the box holds credentials for outside its
	// own country set. Both refusals still land before the FX snapshot and the
	// insert, which is the property this ordering has to keep.
	railImpl, ok := s.rails[rail]
	if !ok {
		return nil, fmt.Errorf("%w: %s (set %s)", ErrRailNotConfigured, rail,
			strings.Join(RailCredentialEnvs[rail], ", "))
	}

	// 3. Require a complete billing profile.
	billingProfile, err := s.profiles.GetBillingProfile(ctx, accountID)
	if err != nil {
		if errors.Is(err, profiles.ErrNotFound) {
			return nil, ErrBillingProfileRequired
		}
		return nil, fmt.Errorf("payments: get billing profile: %w", err)
	}
	if billingProfile.BillingContactName == "" {
		return nil, ErrBillingProfileRequired
	}

	// 4. Tax treatment.
	taxResult := CalculateTax(billingProfile)

	// 5. Compute amounts.
	// amountUSD is in USD cents: credits / (CreditIncrement), where one
	// CreditIncrement is exactly one cent (CreditsPerUSD / 100).
	amountUSD := credits / (CreditsPerUSD / 100)

	var amountLocal int64
	var localCurrency string
	var fxSnapshotID *uuid.UUID

	// 6. BD rails: create FX snapshot and compute BDT paisa amount.
	if isBDRail(rail) {
		snap, err := s.fx.CreateSnapshot(ctx, s.repo, accountID)
		if err != nil {
			return nil, fmt.Errorf("payments: create FX snapshot: %w", err)
		}
		snapID := snap.ID
		fxSnapshotID = &snapID
		localCurrency = "BDT"

		amountLocal, err = usdCentsToLocalPaisa(amountUSD, snap.EffectiveRate)
		if err != nil {
			return nil, fmt.Errorf("payments: %w", err)
		}
	}

	taxAmountLocal := ApplyTax(amountLocal, taxResult)

	// 7. Build and insert intent.
	now := time.Now().UTC()
	intent := PaymentIntent{
		ID:             uuid.New(),
		AccountID:      accountID,
		Rail:           rail,
		Status:         IntentStatusCreated,
		Credits:        credits,
		AmountUSD:      amountUSD,
		AmountLocal:    amountLocal,
		LocalCurrency:  localCurrency,
		FXSnapshotID:   fxSnapshotID,
		TaxTreatment:   taxResult.TaxTreatment,
		TaxRate:        taxResult.TaxRate,
		TaxAmountLocal: taxAmountLocal,
		IdempotencyKey: idempotencyKey,
		// Credit-unit stamp (see ledger.CreditUnitV2): lets the post-deploy
		// straggler detector tell a pre-rescale intent that missed the
		// migration from a native new-unit one.
		Metadata:       map[string]any{"credit_unit": "v2-1usd-1e9"},
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.repo.InsertPaymentIntent(ctx, intent); err != nil {
		return nil, fmt.Errorf("payments: insert intent: %w", err)
	}

	// The insert stays ahead of the provider call deliberately: it is the durable
	// record that any provider-side session this request opens is attributable to
	// an account, and the unique index on (account_id, idempotency_key) is the
	// only thing stopping a double submit from opening two of them. What was
	// missing is the other half, a terminal state for the attempts that fail
	// after it. Every error path below drives the intent to `failed` rather than
	// abandoning it in `created`, so the class of stranded rows this reviewer
	// found has no source instead of needing a reaper to clean up after it (the
	// same shape as the stranded reservation holds of issue #600).
	markFailed := func(cause error) error {
		if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, IntentStatusCreated, IntentStatusFailed); err != nil {
			return errors.Join(cause, fmt.Errorf("payments: mark intent failed: %w", err))
		}
		return cause
	}

	// 8. Call the rail to initiate the provider-side payment.
	initiateInput := InitiateInput{
		PaymentIntentID: intent.ID,
		AccountID:       accountID,
		Credits:         credits,
		AmountUSD:       amountUSD,
		AmountLocal:     amountLocal,
		Currency:        localCurrencyFor(rail),
		CallbackBaseURL: callbackBaseURL,
		ReturnBaseURL:   returnBaseURL,
		CustomerName:    billingProfile.BillingContactName,
		CustomerEmail:   billingProfile.BillingContactEmail,
	}

	initiateResult, err := railImpl.Initiate(ctx, initiateInput)
	if err != nil {
		return nil, markFailed(fmt.Errorf("payments: rail initiate: %w", err))
	}

	// 9. Persist provider details.
	expiresAt := initiateResult.ExpiresAt
	if err := s.repo.UpdateProviderDetails(ctx, intent.ID, initiateResult.ProviderIntentID, initiateResult.RedirectURL, &expiresAt); err != nil {
		return nil, markFailed(fmt.Errorf("payments: update provider details: %w", err))
	}

	// 10. Transition created -> pending_redirect.
	if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, IntentStatusCreated, IntentStatusPendingRedirect); err != nil {
		return nil, fmt.Errorf("payments: transition to pending_redirect: %w", err)
	}

	// Return the updated intent.
	intent.ProviderIntentID = initiateResult.ProviderIntentID
	intent.RedirectURL = initiateResult.RedirectURL
	intent.ExpiresAt = &expiresAt
	intent.Status = IntentStatusPendingRedirect
	return &intent, nil
}

// ---------------------------------------------------------------------------
// GetCheckoutIntent
// ---------------------------------------------------------------------------

// GetCheckoutIntent returns the customer-safe view of one payment intent owned
// by the given account. It backs the browser return page (issue #538).
//
// This is strictly a read. It performs no state transition and posts no ledger
// entry, which is the whole point: a customer who replays or hand-crafts a
// return URL can look at an outcome they already own and cannot cause one.
// Settlement stays with HandleProviderEvent (provider webhook) and
// ConfirmPendingBDPayments (server-side confirmation loop).
//
// An intent belonging to another account is reported as ErrIntentNotFound
// rather than a permission error, so the endpoint cannot be used to probe which
// intent ids exist.
func (s *Service) GetCheckoutIntent(ctx context.Context, accountID, intentID uuid.UUID) (*CheckoutIntentView, error) {
	intent, err := s.repo.GetPaymentIntent(ctx, intentID)
	if err != nil {
		if errors.Is(err, ErrIntentNotFound) {
			return nil, ErrIntentNotFound
		}
		return nil, fmt.Errorf("payments: get intent: %w", err)
	}
	if intent.AccountID != accountID {
		return nil, ErrIntentNotFound
	}

	view := NewCheckoutIntentView(intent)
	return &view, nil
}

// ---------------------------------------------------------------------------
// HandleProviderEvent
// ---------------------------------------------------------------------------

// HandleProviderEvent processes an incoming provider webhook.
//
// Flow:
//  1. Record the inbound delivery (before anything can fail)
//  2. Parse the raw event via the rail
//  3. Look up the intent by provider ID
//  4. Record the payment event
//  5. Transition intent status based on event type
//  6. Post ledger grant on success (Stripe: immediate; BD rails: to confirming)
//  7. Mark the delivery settled
//
// Every error returned from here means the delivery was NOT settled, and the
// caller must tell the provider so (issue #628): a 2xx stops provider retries,
// which was the only mechanism that could recover a failed grant. Retries are
// safe because the grant is idempotent, and every failure leaves the step-1
// record behind as a dead letter.
func (s *Service) HandleProviderEvent(ctx context.Context, rail Rail, rawBody []byte, headers map[string]string) error {
	railImpl, ok := s.rails[rail]
	if !ok {
		return fmt.Errorf("payments: no rail implementation for %s", rail)
	}

	// 1. Persist the delivery first. Until this row exists, a failure anywhere
	// below is invisible rather than merely unrecovered.
	delivery := WebhookDelivery{
		ID:         uuid.New(),
		Rail:       rail,
		Status:     DeliveryStatusReceived,
		RawBody:    string(rawBody),
		ReceivedAt: time.Now().UTC(),
	}
	if err := s.repo.InsertWebhookDelivery(ctx, delivery); err != nil {
		return fmt.Errorf("payments: record webhook delivery: %w", err)
	}

	// 2. Parse the event. A rejected payload (bad signature, unsupported type)
	// is permanent: replaying the same bytes cannot succeed.
	railEvent, err := railImpl.ProcessEvent(ctx, rawBody, headers)
	if err != nil {
		return s.failDelivery(ctx, delivery.ID, nil, "",
			fmt.Errorf("payments: process event: %w: %w", ErrEventRejected, err))
	}

	// 3. Look up the intent. A miss can be a webhook that overtook the intent's
	// own provider-id write, so this is retryable, not rejected.
	intent, err := s.repo.GetPaymentIntentByProviderID(ctx, railEvent.ProviderIntentID)
	if err != nil {
		return s.failDelivery(ctx, delivery.ID, nil, railEvent.EventType,
			fmt.Errorf("payments: get intent by provider ID: %w", err))
	}

	// 4. Record the payment event.
	paymentEvent := PaymentEvent{
		ID:              uuid.New(),
		PaymentIntentID: intent.ID,
		EventType:       railEvent.EventType,
		Rail:            rail,
		RawPayload:      json.RawMessage(railEvent.RawPayload),
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.repo.InsertPaymentEvent(ctx, paymentEvent); err != nil {
		return s.failDelivery(ctx, delivery.ID, &intent.ID, railEvent.EventType,
			fmt.Errorf("payments: insert payment event: %w", err))
	}

	// 5 & 6. Transition based on event type.
	switch railEvent.EventType {
	case "payment.succeeded":
		if rail == RailStripe {
			// Stripe: grant first, then complete (see grantThenComplete).
			if _, err := s.grantThenComplete(ctx, intent, intent.Status); err != nil {
				return s.failDelivery(ctx, delivery.ID, &intent.ID, railEvent.EventType, err)
			}
		} else {
			// BD rails: move to confirming with timestamp. The grant happens in
			// ConfirmPendingBDPayments once the confirmation window elapses.
			if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusConfirming); err != nil {
				return s.failDelivery(ctx, delivery.ID, &intent.ID, railEvent.EventType,
					fmt.Errorf("payments: transition to confirming: %w", err))
			}
			if err := s.repo.SetConfirmingAt(ctx, intent.ID, time.Now().UTC()); err != nil {
				return s.failDelivery(ctx, delivery.ID, &intent.ID, railEvent.EventType,
					fmt.Errorf("payments: set confirming_at: %w", err))
			}
		}

	case "payment.failed":
		if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusFailed); err != nil {
			return s.failDelivery(ctx, delivery.ID, &intent.ID, railEvent.EventType,
				fmt.Errorf("payments: transition to failed: %w", err))
		}

	case "payment.expired":
		if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusExpired); err != nil {
			return s.failDelivery(ctx, delivery.ID, &intent.ID, railEvent.EventType,
				fmt.Errorf("payments: transition to expired: %w", err))
		}

	case "payment.cancelled":
		if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusCancelled); err != nil {
			return s.failDelivery(ctx, delivery.ID, &intent.ID, railEvent.EventType,
				fmt.Errorf("payments: transition to cancelled: %w", err))
		}
	}

	// 7. Settled. The row stays for audit; it leaves the dead-letter view.
	if err := s.repo.UpdateWebhookDelivery(ctx, delivery.ID, DeliveryStatusProcessed, &intent.ID, railEvent.EventType, ""); err != nil {
		return fmt.Errorf("payments: mark webhook delivery processed: %w", err)
	}

	return nil
}

// failDelivery records why a delivery could not be settled and returns the cause
// so the caller can answer the provider with a retryable status. A failure to
// write the dead-letter detail is joined onto the cause rather than swallowed.
func (s *Service) failDelivery(ctx context.Context, deliveryID uuid.UUID, intentID *uuid.UUID, eventType string, cause error) error {
	if err := s.repo.UpdateWebhookDelivery(ctx, deliveryID, DeliveryStatusFailed, intentID, eventType, cause.Error()); err != nil {
		return errors.Join(cause, fmt.Errorf("payments: mark webhook delivery failed: %w", err))
	}
	return cause
}

// grantThenComplete posts the credit grant and only then marks the intent
// completed, reporting whether this call performed the transition.
//
// The order is the fix for issue #628. `completed` is the claim that the
// customer received what they paid for, so it must never be written before the
// grant that makes it true: with the transition first, a grant failure left an
// intent asserting delivery of credits nobody ever received.
//
// Grant-then-transition is safe to repeat. PostPurchaseGrant keys the grant on
// `payment:purchase:<intent id>`, and the ledger enforces that key with the
// credit_idempotency_keys primary key inside the posting transaction, so a
// redelivery re-runs the grant as a no-op and then converges the status. The
// window this leaves (credits granted, status not yet completed) errs toward the
// customer being served and is closed by the next delivery or confirmation pass.
func (s *Service) grantThenComplete(ctx context.Context, intent PaymentIntent, from IntentStatus) (bool, error) {
	if err := s.PostPurchaseGrant(ctx, intent); err != nil {
		return false, fmt.Errorf("payments: post grant: %w", err)
	}
	transitioned, err := s.repo.CompareAndSetStatus(ctx, intent.ID, from, IntentStatusCompleted)
	if err != nil {
		return false, fmt.Errorf("payments: transition to completed: %w", err)
	}
	return transitioned, nil
}

// ---------------------------------------------------------------------------
// ConfirmPendingBDPayments
// ---------------------------------------------------------------------------

// ConfirmPendingBDPayments finds confirming intents older than 3 minutes,
// transitions them to completed, and posts ledger grants.
// Returns the number of intents confirmed.
func (s *Service) ConfirmPendingBDPayments(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-3 * time.Minute)
	intents, err := s.repo.ListConfirmingIntents(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("payments: list confirming intents: %w", err)
	}

	confirmed := 0
	for _, intent := range intents {
		// Grant first, then complete — same ordering invariant as the webhook
		// path (issue #628). A concurrent worker that already completed this
		// intent makes the grant a no-op and the transition a false, so nothing
		// is double-granted and nothing is double-counted.
		transitioned, err := s.grantThenComplete(ctx, intent, IntentStatusConfirming)
		if err != nil {
			return confirmed, fmt.Errorf("payments: confirm intent %s: %w", intent.ID, err)
		}
		if transitioned {
			confirmed++
		}
	}

	return confirmed, nil
}

// ---------------------------------------------------------------------------
// PostPurchaseGrant
// ---------------------------------------------------------------------------

// PostPurchaseGrant posts a credit grant to the ledger for a completed payment.
// Uses a deterministic idempotency key to prevent double-crediting.
func (s *Service) PostPurchaseGrant(ctx context.Context, intent PaymentIntent) error {
	idempotencyKey := fmt.Sprintf("payment:purchase:%s", intent.ID)

	// Customer-visible ledger metadata. PHASE-17-INTERNAL-ONLY note: keys here
	// land on `GET /api/v1/accounts/current/ledger/entries` for the account.
	// `payment_intent_id` is sufficient audit linkage to reconstruct the FX
	// snapshot internally (payment_intents.fx_snapshot_id is server-side
	// only), so we MUST NOT propagate `fx_snapshot_id` here — that would
	// leak an `fx_*` key onto a BD customer surface and violate the FX/USD
	// zero-leak contract (FX-17 adversarial-review finding, 2026-05-14).
	metadata := map[string]any{
		"payment_intent_id": intent.ID.String(),
		"rail":              string(intent.Rail),
		"tax_treatment":     intent.TaxTreatment,
	}

	_, err := s.ledger.GrantCredits(ctx, intent.AccountID, idempotencyKey, intent.Credits, metadata)
	if err != nil {
		return fmt.Errorf("payments: grant credits: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GetCheckoutOptions
// ---------------------------------------------------------------------------

// CheckoutOptions holds available rails and predefined tiers for a checkout session.
//
// Phase 17 FX/USD zero-leak (FX-17-03): per-country pricing primitive.
//   - PricePerBlockMinor: minor units of resolved currency per
//     `CreditBlockSize` credits (paisa for BDT, cents for USD).
//   - CreditBlockSize: number of credits that one block of `PricePerBlockMinor`
//     pays for. Mirrors `CreditsPerUSD` (1,000,000,000 credits =
//     1 USD-equivalent since the 2026-08-23 credit unit rescale).
//   - Currency: ISO 4217 code of the resolved currency. "BDT" for BD
//     accounts, "USD" otherwise.
//
// Front-end renders a localised total using integer arithmetic:
//
//	total_minor = floor(credits * PricePerBlockMinor / CreditBlockSize)
//
// FX rate is NEVER exposed in the response. The server resolves USD →
// local-currency minor units via `math/big` against the latest FX
// snapshot mid-rate (with the standard fee markup) and returns only the
// resolved scalar.
// CreditIncrement, MinCredits and MaxCredits repeat the purchase bounds at the
// top level because that is where the console reads them
// (`apps/web-console/lib/control-plane/client.ts`). Their absence was not
// visible: the client silently substituted a fallback ceiling of
// 1,000,000,000 credits, which is 1.00 USD, against a real Stripe ceiling of
// 100.00 USD (issue #1386).
//
// MaxCredits is the MINIMUM ceiling across the rails on offer, not the maximum.
// The console carries one bound for a rail the payer has not chosen yet, so the
// only value that cannot advertise more than a rail will accept is the most
// restrictive one. ValidatePurchaseAmount remains the authority and still
// enforces the real per-rail ceiling on initiate, so this wire value can only
// ever be stricter than enforcement, never looser.
type CheckoutOptions struct {
	Rails              []RailOption `json:"rails"`
	PredefinedTiers    []int64      `json:"predefined_tiers"`
	PricePerBlockMinor int64        `json:"price_per_block_minor"`
	CreditBlockSize    int64        `json:"credit_block_size"`
	Currency           string       `json:"currency"`
	CreditIncrement    int64        `json:"credit_increment"`
	MinCredits         int64        `json:"min_credits"`
	MaxCredits         int64        `json:"max_credits"`
}

// RailOption describes a single payment rail with its credit limits.
//
// Currency, Label and Enabled exist because the console's decoder requires them
// and drops any rail item that lacks one, which emptied the rail list and left
// the checkout modal with no payment method to offer (issue #1386).
//
// Enabled is whether this deployment can actually execute a checkout on the
// rail, which is exactly whether the rail was registered at startup from its
// credentials. A rail that cannot complete a purchase must not present itself
// as a choice.
type RailOption struct {
	Rail       Rail   `json:"rail"`
	Label      string `json:"label"`
	Currency   string `json:"currency"`
	Enabled    bool   `json:"enabled"`
	MinCredits int64  `json:"min_credits"`
	MaxCredits int64  `json:"max_credits"`
}

// RailLabel returns the customer-facing name of a payment rail.
//
// The provider-blindness rule this repository enforces is about inference
// providers: the customer must never learn which upstream model host served
// their request. A payment rail is the opposite situation. The payer is
// choosing the rail deliberately and is redirected to that rail's own checkout
// page under its own branding, so a BD customer selecting "bKash" has to see
// the word bKash or the choice is meaningless. The `rail` field on this same
// wire object has carried "bkash" and "sslcommerz" since the endpoint existed,
// so a neutral label would hide nothing anyway.
//
// Stripe is labelled "Card" because that is what the customer is actually
// picking there, not because the name needs hiding.
//
// The default never fires for a Rail this package defines, and returns a
// generic string rather than the raw identifier so that an unrecognised value
// can never be echoed straight back onto a customer surface.
func RailLabel(r Rail) string {
	switch r {
	case RailStripe:
		return "Card"
	case RailBkash:
		return "bKash"
	case RailSSLCommerz:
		return "SSLCommerz"
	default:
		return "Payment method"
	}
}

// NewRailOption builds the wire option for one rail. `enabled` says whether the
// deployment can actually execute a checkout on it. Defined once so the real
// service and the demo stub cannot drift into two different payload shapes.
func NewRailOption(rail Rail, enabled bool) RailOption {
	return RailOption{
		Rail:       rail,
		Label:      RailLabel(rail),
		Currency:   localCurrencyFor(rail),
		Enabled:    enabled,
		MinCredits: MinPurchaseCredits,
		MaxCredits: maxCreditsForRail(rail),
	}
}

// MostRestrictiveMaxCredits returns the smallest per-rail purchase ceiling among
// the options the payer can actually select, which is the only single ceiling
// that no selectable rail will reject.
//
// Disabled options are skipped. Including them would let a rail nobody can
// choose set the bound: on a deployment with bKash registered and Stripe not,
// the disabled Stripe entry carries the smallest ceiling and would cap the
// console at 100.00 USD while the only selectable rail accepts 300.00 USD.
//
// Returns 0 when nothing is selectable, which is the honest answer for a
// deployment that has no usable rail, and which ValidatePurchaseAmount refuses
// anyway since every purchase must be positive.
func MostRestrictiveMaxCredits(options []RailOption) int64 {
	var minMax int64
	for _, opt := range options {
		if !opt.Enabled {
			continue
		}
		if minMax == 0 || opt.MaxCredits < minMax {
			minMax = opt.MaxCredits
		}
	}
	return minMax
}

// GetCheckoutOptions returns available payment rails, predefined tiers, and
// the per-country resolved pricing primitive for the account.
//
// Branching:
//   - BD accounts → resolve via FX snapshot to BDT paisa using `math/big`.
//     `PricePerBlockMinor = effectiveRate * 100` paisa per
//     `CreditBlockSize` (= `CreditsPerUSD`) credits.
//   - non-BD accounts → 100 cents per `CreditBlockSize` (= `CreditsPerUSD`)
//     credits (1 USD = 100 cents, computed via `math/big` for parity with
//     the BD path — no `float64` arithmetic on the resolved value).
//
// FX rate is computed server-side only and never returned. If FX is
// unavailable for a BD account, the FX provider error surfaces.
func (s *Service) GetCheckoutOptions(ctx context.Context, accountID uuid.UUID) (*CheckoutOptions, error) {
	countryCode, err := s.profiles.CountryCode(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("payments: get account country: %w", err)
	}

	available := AvailableRails(countryCode)
	railOptions := make([]RailOption, 0, len(available))
	for _, rail := range available {
		// A rail is offered only if this deployment registered it, which it
		// does from the rail's credentials at startup. Advertising an
		// unregistered rail would hand the payer a choice that InitiateCheckout
		// refuses.
		_, configured := s.rails[rail]
		railOptions = append(railOptions, NewRailOption(rail, configured))
	}

	priceMinor, currency, err := s.resolvePricePerUSDBlock(ctx, countryCode, accountID)
	if err != nil {
		return nil, fmt.Errorf("payments: resolve price per credit: %w", err)
	}

	return &CheckoutOptions{
		Rails:              railOptions,
		PredefinedTiers:    PredefinedTiers,
		PricePerBlockMinor: priceMinor,
		CreditBlockSize:    CreditsPerUSD,
		Currency:           currency,
		CreditIncrement:    CreditIncrement,
		MinCredits:         MinPurchaseCredits,
		MaxCredits:         MostRestrictiveMaxCredits(railOptions),
	}, nil
}

// resolvePricePerUSDBlock returns the resolved minor-units price for one
// `CreditsPerUSD` block of credits (i.e. 1 USD-equivalent), and the ISO
// 4217 currency code, branching on the account's country.
//
// All arithmetic uses `math/big` to avoid float64 corruption (per
// `CLAUDE.md` math/big mandate for financial calcs).
func (s *Service) resolvePricePerUSDBlock(ctx context.Context, countryCode string, accountID uuid.UUID) (int64, string, error) {
	if countryCode == "BD" {
		// 1 USD = 100 cents. BD: convert via FX snapshot to BDT paisa.
		// paisa_per_block = effectiveRate * 100.
		snap, err := s.fx.CreateSnapshot(ctx, s.repo, accountID)
		if err != nil {
			return 0, "", fmt.Errorf("payments: create FX snapshot: %w", err)
		}
		effectiveRat := new(big.Rat)
		if _, ok := effectiveRat.SetString(snap.EffectiveRate); !ok {
			return 0, "", fmt.Errorf("payments: invalid effective rate %q", snap.EffectiveRate)
		}
		paisaRat := new(big.Rat).Mul(effectiveRat, new(big.Rat).SetInt64(100))
		// Integer truncation via math/big (no float64): floor(num/den).
		paisa := new(big.Int).Quo(paisaRat.Num(), paisaRat.Denom())
		return paisa.Int64(), "BDT", nil
	}
	// Non-BD: 1 USD-equivalent block = 100 cents. math/big for parity.
	cents := new(big.Int).SetInt64(100)
	return cents.Int64(), "USD", nil
}

// maxCreditsForRail returns the maximum purchasable credits for a given rail.
func maxCreditsForRail(r Rail) int64 {
	switch r {
	case RailBkash:
		return MaxPurchaseCreditsBkash
	case RailSSLCommerz:
		return MaxPurchaseCreditsSSLCommerz
	default:
		return MaxPurchaseCreditsStripe
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func railIn(r Rail, available []Rail) bool {
	for _, a := range available {
		if a == r {
			return true
		}
	}
	return false
}

func isBDRail(r Rail) bool {
	return r == RailBkash || r == RailSSLCommerz
}

func localCurrencyFor(r Rail) string {
	if isBDRail(r) {
		return "BDT"
	}
	return "USD"
}

// usdCentsToLocalPaisa converts a USD-cent amount to integer BDT paisa
// using the effective FX rate, entirely in exact rational arithmetic.
//
// amountPaisa = amountUSDcents/100 (dollars) * rate (BDT/USD) * 100 (paisa)
//
//	= amountUSDcents * rate
//
// The result is truncated to an integer via big.Int division on the
// numerator/denominator (truncates toward zero — correct for the
// non-negative amounts handled here). No float64 ever appears: routing
// the value through Float64() would reintroduce the binary-fraction error
// that the math/big FX invariant (CLAUDE.md) exists to prevent.
func usdCentsToLocalPaisa(amountUSDCents int64, effectiveRate string) (int64, error) {
	rateRat, ok := new(big.Rat).SetString(effectiveRate)
	if !ok {
		return 0, fmt.Errorf("invalid effective rate %q", effectiveRate)
	}
	localRat := new(big.Rat).Mul(new(big.Rat).SetInt64(amountUSDCents), rateRat)
	return new(big.Int).Quo(localRat.Num(), localRat.Denom()).Int64(), nil
}
