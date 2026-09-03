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

	// 5 and 6. Compute amounts, in the fixed order purchase_price.go documents:
	// credits, then USD at the peg, then the 6 percent markup, then FX for a
	// local-currency payer, then one truncation per currency. The FX snapshot
	// has to exist before the price does on a BD rail, so the two steps are one
	// block rather than two.
	//
	// A USD purchase reaches PriceForCredits with an empty rate and therefore
	// takes no FX markup at all, which is the property issue #1693 asks to be
	// tested specifically.
	var effectiveRate string
	var localCurrency string
	var fxSnapshotID *uuid.UUID

	if isBDRail(rail) {
		snap, err := s.fx.CreateSnapshot(ctx, s.repo, accountID)
		if err != nil {
			return nil, fmt.Errorf("payments: create FX snapshot: %w", err)
		}
		snapID := snap.ID
		fxSnapshotID = &snapID
		localCurrency = "BDT"
		effectiveRate = snap.EffectiveRate
	}

	price, err := PriceForCredits(credits, effectiveRate)
	if err != nil {
		return nil, err
	}
	amountUSD := price.USDCents
	amountLocal := price.LocalMinor

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
		//
		// purchase_markup_rate is the second stamp and it is here for the same
		// class of reason. The markup is a constant today, so a reader could
		// recompute this row from the constant in force when they read it,
		// which is exactly the assumption that made issue #1682 possible: the
		// rate on the row is the rate that was applied to the row. The FX half
		// of the chain is already recorded, on the fx_snapshots row
		// fx_snapshot_id points at, so amount_local reproduces from
		// credits, purchase_markup_rate and that snapshot's effective_rate with
		// no constant from the current build involved.
		Metadata: map[string]any{
			"credit_unit":          "v2-1usd-1e9",
			"purchase_markup_rate": price.MarkupRate,
		},
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
// The price of a purchase lives on the RAIL (see RailOption), not here. It used
// to be one per-account scalar, resolved from the account's country, and that
// was wrong in both directions for a BD account: the country says taka while
// InitiateCheckout charges in whatever currency the SELECTED rail settles in,
// and a BD account is offered Stripe alongside bKash and SSLCommerz
// (AvailableRails). A single figure cannot be right for a dollar rail and a
// taka rail at once (issue #1737).
//
// The FX rate itself is still never exposed. What a rail publishes is the
// resolved price of a credit in its own currency, with the markup already
// folded in and no way to read the rate back out of it.
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
	Rails           []RailOption `json:"rails"`
	PredefinedTiers []int64      `json:"predefined_tiers"`
	CreditIncrement int64        `json:"credit_increment"`
	MinCredits      int64        `json:"min_credits"`
	MaxCredits      int64        `json:"max_credits"`
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

	// PriceMinorNumerator and PriceCreditsDenominator are the EXACT price of one
	// credit on this rail, in this rail's own Currency, as a fraction:
	//
	//	amount_minor = floor(credits * PriceMinorNumerator / PriceCreditsDenominator)
	//
	// The console applies that formula to the quantity the payer typed and
	// renders the result, so what the modal shows is what InitiateCheckout
	// charges. Both halves of the fraction are whole numbers below 2^53 - 1, so
	// JavaScript reads them exactly and does the division in BigInt.
	//
	// A fraction rather than a scalar because a scalar cannot be exact. The
	// previous wire sent the price of one CreditsPerUSD block already truncated
	// to whole minor units and let the console multiply it by the block count.
	// That is exact in dollars, where a block costs a whole 106 cents, and it
	// stopped being exact in taka when D-066 replaced the 5 percent FX markup
	// with 2.5 percent: a mid rate of 127 becomes an effective 130.175, a block
	// costs 13798.55 paisa, and dropping that half paisa on every block
	// under-quoted the payer a little more with every block they bought.
	PriceMinorNumerator     int64 `json:"price_minor_numerator"`
	PriceCreditsDenominator int64 `json:"price_credits_denominator"`
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
//
// effectiveRate is the FX snapshot's effective rate, and it is REQUIRED for a
// rail that settles in local currency. Pricing a taka rail without one would
// quote the dollar figure under a taka label, which is the same defect the
// per-account price had, only off by the whole exchange rate. It is ignored for
// a rail that settles in USD, which does no conversion and takes no FX markup.
func NewRailOption(rail Rail, enabled bool, effectiveRate string) (RailOption, error) {
	rate := ""
	if isBDRail(rail) {
		if effectiveRate == "" {
			return RailOption{}, fmt.Errorf("payments: rail %s settles in local currency and needs an effective FX rate", rail)
		}
		rate = effectiveRate
	}

	numerator, denominator, err := railPriceFraction(rate)
	if err != nil {
		return RailOption{}, err
	}

	return RailOption{
		Rail:                    rail,
		Label:                   RailLabel(rail),
		Currency:                localCurrencyFor(rail),
		Enabled:                 enabled,
		MinCredits:              MinPurchaseCredits,
		MaxCredits:              maxCreditsForRail(rail),
		PriceMinorNumerator:     numerator,
		PriceCreditsDenominator: denominator,
	}, nil
}

// maxSafeJSNumber is 2^53 - 1, the largest integer a JSON number survives
// intact in a browser. Every JSON number is a float64 there, so a value past
// this arrives at the console silently rounded.
const maxSafeJSNumber int64 = 9007199254740991

// railPriceFraction turns the exact per-credit price into the whole-number
// fraction the console divides with, in lowest terms.
//
// The bound check is the point of the function. big.Rat will happily hand back
// a numerator with forty digits for a pathological rate, JSON will happily
// serialise it, and the browser will silently round it and price the purchase
// wrong with nothing to show for it. Refusing to answer is the failure a
// customer and an on-call engineer can both see; a quietly rounded price is
// not. Real rates come nowhere near the bound: FXService formats the effective
// rate to six decimal places, which caps the denominator at CreditIncrement x
// 100 x 1e6 = 1e15 before reduction.
func railPriceFraction(effectiveRate string) (int64, int64, error) {
	rate, err := PriceRatePerCredit(effectiveRate)
	if err != nil {
		return 0, 0, err
	}
	num, den := rate.Num(), rate.Denom()
	if !num.IsInt64() || !den.IsInt64() {
		return 0, 0, fmt.Errorf("payments: price fraction %s does not fit in an int64", rate.RatString())
	}
	n, d := num.Int64(), den.Int64()
	if n <= 0 || n > maxSafeJSNumber || d <= 0 || d > maxSafeJSNumber {
		return 0, 0, fmt.Errorf("payments: price fraction %d/%d is outside the range a JSON number carries exactly", n, d)
	}
	return n, d, nil
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

// GetCheckoutOptions returns the payment rails available to the account, the
// predefined tiers, the purchase bounds, and the resolved price of a credit on
// each rail.
//
// One FX snapshot is taken for the whole call, and only when the account is
// offered a rail that settles in local currency. Both BD rails then quote from
// the same rate, which is also what a purchase on either of them will be
// charged at: FXService caches the mid rate for five minutes, so a quote and
// the checkout that follows it agree unless the payer sits on the modal longer
// than that.
//
// The rate is never returned. What leaves this function is the resolved price
// of one credit on one rail, with the 6 percent purchase markup (D-065) and the
// 2.5 percent FX markup (D-066) already folded in and not separable, which is
// what D-066 requires: the payer sees one price and no fee line.
func (s *Service) GetCheckoutOptions(ctx context.Context, accountID uuid.UUID) (*CheckoutOptions, error) {
	countryCode, err := s.profiles.CountryCode(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("payments: get account country: %w", err)
	}

	available := AvailableRails(countryCode)

	effectiveRate := ""
	for _, rail := range available {
		if !isBDRail(rail) {
			continue
		}
		snap, err := s.fx.CreateSnapshot(ctx, s.repo, accountID)
		if err != nil {
			return nil, fmt.Errorf("payments: create FX snapshot: %w", err)
		}
		effectiveRate = snap.EffectiveRate
		break
	}

	railOptions := make([]RailOption, 0, len(available))
	for _, rail := range available {
		// A rail is offered only if this deployment registered it, which it
		// does from the rail's credentials at startup. Advertising an
		// unregistered rail would hand the payer a choice that InitiateCheckout
		// refuses.
		_, configured := s.rails[rail]
		opt, err := NewRailOption(rail, configured, effectiveRate)
		if err != nil {
			return nil, fmt.Errorf("payments: price rail %s: %w", rail, err)
		}
		railOptions = append(railOptions, opt)
	}

	return &CheckoutOptions{
		Rails:           railOptions,
		PredefinedTiers: PredefinedTiers,
		CreditIncrement: CreditIncrement,
		MinCredits:      MinPurchaseCredits,
		MaxCredits:      MostRestrictiveMaxCredits(railOptions),
	}, nil
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
// The amount is a *big.Rat rather than an int64 because the purchase price it
// converts is fractional before it is rounded: the markup lands on an exact
// cent figure and the conversion has to happen on THAT, not on a figure already
// truncated to whole cents, or the local price would carry two roundings
// (purchase_price.go, step 4). A whole-cent caller passes an integral rational
// and gets the same answer it always did.
//
// The result is truncated to an integer via big.Int division on the
// numerator/denominator (truncates toward zero — correct for the
// non-negative amounts handled here). No float64 ever appears: routing
// the value through Float64() would reintroduce the binary-fraction error
// that the math/big FX invariant (CLAUDE.md) exists to prevent.
func usdCentsToLocalPaisa(amountUSDCents *big.Rat, effectiveRate string) (int64, error) {
	rateRat, ok := new(big.Rat).SetString(effectiveRate)
	if !ok {
		return 0, fmt.Errorf("invalid effective rate %q", effectiveRate)
	}
	// A zero or negative rate is refused rather than used. It cannot arise from
	// the XE response today, but it can arise from the operator override that
	// stands in for it, and the two failures it would produce are the two this
	// money path must never have: a purchase priced at nothing, and a purchase
	// priced at a negative amount. Neither would look like a failure downstream,
	// which is why it is caught here rather than left to be noticed.
	if rateRat.Sign() <= 0 {
		return 0, fmt.Errorf("effective rate %q is zero or negative; that is not a rate", effectiveRate)
	}
	localRat := new(big.Rat).Mul(amountUSDCents, rateRat)
	paisa, err := truncateToMinorUnit(localRat)
	if err != nil {
		return 0, fmt.Errorf("local price: %w", err)
	}
	return paisa, nil
}
