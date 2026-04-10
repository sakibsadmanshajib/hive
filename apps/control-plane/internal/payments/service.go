package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
)

// ---------------------------------------------------------------------------
// Dependency interfaces (accept interfaces, return structs)
// ---------------------------------------------------------------------------

// LedgerGranter posts credit grant entries to the ledger.
type LedgerGranter interface {
	GrantCredits(ctx context.Context, accountID uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error)
}

// ProfileReader reads account and billing profiles.
type ProfileReader interface {
	GetBillingProfile(ctx context.Context, accountID uuid.UUID) (profiles.BillingProfile, error)
	GetAccountProfile(ctx context.Context, accountID uuid.UUID) (profiles.AccountProfile, error)
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
//  2. Verify rail is available for the account's country
//  3. Load and validate billing profile (gate)
//  4. Calculate tax treatment
//  5. Compute amounts (USD cents, local paisa for BD)
//  6. Create FX snapshot for BD rails
//  7. Insert payment intent
//  8. Call rail.Initiate
//  9. Update provider details
//  10. Transition created -> pending_redirect
func (s *Service) InitiateCheckout(ctx context.Context, accountID uuid.UUID, rail Rail, credits int64, callbackBaseURL, idempotencyKey string) (*PaymentIntent, error) {
	// 1. Validate credits.
	if err := ValidatePurchaseAmount(credits); err != nil {
		return nil, err
	}

	// 2. Verify rail availability for the account's country.
	accountProfile, err := s.profiles.GetAccountProfile(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("payments: get account profile: %w", err)
	}
	available := AvailableRails(accountProfile.CountryCode)
	if !railIn(rail, available) {
		return nil, fmt.Errorf("payments: rail %s not available for country %s", rail, accountProfile.CountryCode)
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
	// amountUSD is in USD cents: credits / (CreditsPerUSD / 100)
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

		// amountLocal (paisa) = (amountUSD cents / 100) * effectiveRate * 100
		// = amountUSD * effectiveRate
		effectiveRat := new(big.Rat)
		if _, ok := effectiveRat.SetString(snap.EffectiveRate); !ok {
			return nil, fmt.Errorf("payments: invalid effective rate %q", snap.EffectiveRate)
		}
		amountUSDRat := new(big.Rat).SetInt64(amountUSD)
		// cents to dollars then multiply by rate then to paisa: amountUSD/100 * rate * 100 = amountUSD * rate
		localRat := new(big.Rat).Mul(amountUSDRat, effectiveRat)
		localFloat, _ := localRat.Float64()
		amountLocal = int64(localFloat)
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
		Metadata:       map[string]any{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.repo.InsertPaymentIntent(ctx, intent); err != nil {
		return nil, fmt.Errorf("payments: insert intent: %w", err)
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
		CustomerName:    billingProfile.BillingContactName,
		CustomerEmail:   billingProfile.BillingContactEmail,
	}

	railImpl, ok := s.rails[rail]
	if !ok {
		return nil, fmt.Errorf("payments: no rail implementation for %s", rail)
	}
	initiateResult, err := railImpl.Initiate(ctx, initiateInput)
	if err != nil {
		return nil, fmt.Errorf("payments: rail initiate: %w", err)
	}

	// 9. Persist provider details.
	expiresAt := initiateResult.ExpiresAt
	if err := s.repo.UpdateProviderDetails(ctx, intent.ID, initiateResult.ProviderIntentID, initiateResult.RedirectURL, &expiresAt); err != nil {
		return nil, fmt.Errorf("payments: update provider details: %w", err)
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
// HandleProviderEvent
// ---------------------------------------------------------------------------

// HandleProviderEvent processes an incoming provider webhook.
//
// Flow:
//  1. Parse the raw event via the rail
//  2. Look up the intent by provider ID
//  3. Record the payment event
//  4. Transition intent status based on event type
//  5. Post ledger grant on success (Stripe: immediate; BD rails: to confirming)
func (s *Service) HandleProviderEvent(ctx context.Context, rail Rail, rawBody []byte, headers map[string]string) error {
	railImpl, ok := s.rails[rail]
	if !ok {
		return fmt.Errorf("payments: no rail implementation for %s", rail)
	}

	// 1. Parse the event.
	railEvent, err := railImpl.ProcessEvent(ctx, rawBody, headers)
	if err != nil {
		return fmt.Errorf("payments: process event: %w", err)
	}

	// 2. Look up the intent.
	intent, err := s.repo.GetPaymentIntentByProviderID(ctx, railEvent.ProviderIntentID)
	if err != nil {
		return fmt.Errorf("payments: get intent by provider ID: %w", err)
	}

	// 3. Record the payment event.
	paymentEvent := PaymentEvent{
		ID:              uuid.New(),
		PaymentIntentID: intent.ID,
		EventType:       railEvent.EventType,
		Rail:            rail,
		RawPayload:      json.RawMessage(railEvent.RawPayload),
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.repo.InsertPaymentEvent(ctx, paymentEvent); err != nil {
		return fmt.Errorf("payments: insert payment event: %w", err)
	}

	// 4 & 5. Transition based on event type.
	switch railEvent.EventType {
	case "payment.succeeded":
		if rail == RailStripe {
			// Stripe: immediate complete + grant.
			if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusCompleted); err != nil {
				return fmt.Errorf("payments: transition to completed: %w", err)
			}
			if err := s.PostPurchaseGrant(ctx, intent); err != nil {
				return fmt.Errorf("payments: post grant: %w", err)
			}
		} else {
			// BD rails: move to confirming with timestamp.
			if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusConfirming); err != nil {
				return fmt.Errorf("payments: transition to confirming: %w", err)
			}
			if err := s.repo.SetConfirmingAt(ctx, intent.ID, time.Now().UTC()); err != nil {
				return fmt.Errorf("payments: set confirming_at: %w", err)
			}
		}

	case "payment.failed":
		if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusFailed); err != nil {
			return fmt.Errorf("payments: transition to failed: %w", err)
		}

	case "payment.expired":
		if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusExpired); err != nil {
			return fmt.Errorf("payments: transition to expired: %w", err)
		}

	case "payment.cancelled":
		if _, err := s.repo.CompareAndSetStatus(ctx, intent.ID, intent.Status, IntentStatusCancelled); err != nil {
			return fmt.Errorf("payments: transition to cancelled: %w", err)
		}
	}

	return nil
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
		transitioned, err := s.repo.CompareAndSetStatus(ctx, intent.ID, IntentStatusConfirming, IntentStatusCompleted)
		if err != nil {
			return confirmed, fmt.Errorf("payments: confirm intent %s: %w", intent.ID, err)
		}
		if !transitioned {
			// Already transitioned by a concurrent worker — skip grant.
			continue
		}

		if err := s.PostPurchaseGrant(ctx, intent); err != nil {
			return confirmed, fmt.Errorf("payments: post grant for intent %s: %w", intent.ID, err)
		}
		confirmed++
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

	metadata := map[string]any{
		"payment_intent_id": intent.ID.String(),
		"rail":              string(intent.Rail),
		"tax_treatment":     intent.TaxTreatment,
	}
	if intent.FXSnapshotID != nil {
		metadata["fx_snapshot_id"] = intent.FXSnapshotID.String()
	}

	_, err := s.ledger.GrantCredits(ctx, intent.AccountID, idempotencyKey, intent.Credits, metadata)
	if err != nil {
		return fmt.Errorf("payments: grant credits: %w", err)
	}
	return nil
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
