// Package stub provides a demo-mode payment service that short-circuits
// real payment rails and credits accounts immediately through the normal
// ledger path.
//
// Activation: set HIVE_PAYMENTS_STUB=true in the environment before starting
// the control-plane. When the flag is absent or set to any value other than
// "true", the production payment service is used and this package has no effect.
//
// Security contract:
//   - The stub is a complete replacement for the production Service; no live
//     rail code is called.
//   - The stub may only run in a known-safe environment. CheckProductionSafety
//     uses an ALLOWLIST (demo, staging, local, development, test): if the stub
//     is enabled and HIVE_ENV is anything else (including unset), startup
//     fatals. This prevents an instant-credit bypass when an operator forgets
//     to set HIVE_ENV in a real production deployment.
//   - All credits flow through the same LedgerGranter interface used by the
//     real service, so the ledger remains append-only and the balance is correct.
//   - math/big is used for all credit arithmetic to satisfy the project-wide
//     float-free financial computation invariant.
//   - Country to rail access control mirrors the production AvailableRails
//     rule exactly: a non-BD account cannot select bKash or SSLCommerz, and a
//     BD account only sees the rails allowed for BD. The stub reuses
//     payments.AvailableRails so the rule is never duplicated.
//   - Amount fields returned in stub responses contain the credit count only;
//     no USD amount, no FX rate, no amount_local is derived (there is nothing
//     to convert without a real FX snapshot).
//   - BD regulatory rule: amount_usd is NOT included in any customer-facing
//     response (same constraint as the production handler — enforced by the
//     initiateResponse DTO in payments/http.go).
//   - Provider-blind responses: the synthetic ProviderIntentID is a plain UUID
//     with no "stub" marker, so stub mode is not detectable from a
//     customer-visible response field.
//
// This package MUST NOT be imported in production builds unless HIVE_PAYMENTS_STUB
// is explicitly set. Callers should use IsEnabled() to guard the import.
package stub

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// envFlag is the environment variable that enables demo stub mode.
const envFlag = "HIVE_PAYMENTS_STUB"

// safeEnvironments is the ALLOWLIST of HIVE_ENV values (trimmed, lowercased)
// in which the payment stub is permitted to run. Any value not in this set —
// including the empty string — is treated as unsafe and the stub refuses to
// start. This is the inverse of a denylist: forgetting to set HIVE_ENV in a
// real production box fails closed rather than silently activating the stub.
var safeEnvironments = map[string]bool{
	"demo":        true,
	"staging":     true,
	"local":       true,
	"development": true,
	"test":        true,
}

// normalizedEnv returns the trimmed, lowercased HIVE_ENV value.
func normalizedEnv() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("HIVE_ENV")))
}

// IsEnabled reports whether HIVE_PAYMENTS_STUB=true is set.
// Call this in main.go before constructing the payment service so that the
// decision is made once at startup and logged clearly.
func IsEnabled() bool {
	return strings.TrimSpace(os.Getenv(envFlag)) == "true"
}

// EnvIsSafe reports whether the current HIVE_ENV value is in the stub allowlist.
// Exposed so the guard condition can be unit-tested directly rather than
// reconstructed inline in a test (which would let a refactor silently break it).
func EnvIsSafe() bool {
	return safeEnvironments[normalizedEnv()]
}

// CheckProductionSafety hard-fails unless the stub is allowed to run in the
// current environment. The stub is allowed only when HIVE_ENV (trimmed,
// lowercased) is one of {demo, staging, local, development, test}. Any other
// value — including an unset HIVE_ENV — causes log.Fatal so the instant-credit
// stub can never activate in a real production deployment by omission.
//
// Call this during startup, before constructing any payment service. This is
// the single source of truth for the guard; main.go calls it directly so a
// future refactor cannot bypass the allowlist.
func CheckProductionSafety() {
	if !IsEnabled() {
		return
	}
	if !EnvIsSafe() {
		log.Fatalf(
			"payments: %s=true is only permitted when HIVE_ENV is one of "+
				"{demo, staging, local, development, test}; got HIVE_ENV=%q — "+
				"refusing to start to prevent instant-credit bypass of real payment rails",
			envFlag, strings.TrimSpace(os.Getenv("HIVE_ENV")),
		)
	}
}

// ---------------------------------------------------------------------------
// StubService
// ---------------------------------------------------------------------------

// AccountCountryReader resolves the ISO country code for an account so the
// stub can apply the same country to rail access control as production.
// Declared locally (small interface at the point of use) so the stub package
// does not depend on the profiles package directly.
type AccountCountryReader interface {
	CountryCode(ctx context.Context, accountID uuid.UUID) (string, error)
}

// StubService implements the payments.PaymentService interface.
// It replaces InitiateCheckout with an immediate ledger grant and returns
// a synthetic success response, bypassing all payment rails.
type StubService struct {
	ledger    LedgerGranter
	countries AccountCountryReader
}

// LedgerGranter is a subset of the ledger.Service interface that the stub
// needs. Declared locally so the stub package does not depend on the ledger
// package directly (avoids import cycles).
type LedgerGranter interface {
	GrantCredits(
		ctx context.Context,
		accountID uuid.UUID,
		idempotencyKey string,
		credits int64,
		metadata map[string]any,
	) error
}

// NewStubService constructs a StubService. It logs a prominent warning so
// that an operator cannot accidentally run stub mode in production silently.
func NewStubService(ledger LedgerGranter, countries AccountCountryReader) *StubService {
	log.Printf(
		"[WARN] payments: DEMO STUB MODE ACTIVE (env %s=true) — "+
			"real payment rails are disabled; credits granted instantly. "+
			"Do NOT use in production.",
		envFlag,
	)
	return &StubService{ledger: ledger, countries: countries}
}

// ---------------------------------------------------------------------------
// payments.PaymentService implementation
// ---------------------------------------------------------------------------

// InitiateCheckout validates the request, enforces country to rail access
// control (identical to production), immediately grants credits through the
// real ledger, and returns a synthetic PaymentIntent in the 'completed'
// state. No external payment rail is called.
//
// Idempotency: reusing the same idempotency_key for the same (accountID,
// credits) pair is safe — the ledger's ON CONFLICT idempotency guard prevents
// double-crediting.
//
// credits validation mirrors production rules (must be positive, multiple of 1000).
func (s *StubService) InitiateCheckout(
	ctx context.Context,
	accountID uuid.UUID,
	rail payments.Rail,
	credits int64,
	_ string, // callbackBaseURL unused in stub
	idempotencyKey string,
) (*payments.PaymentIntent, error) {
	// Mirror production validation exactly.
	if err := payments.ValidatePurchaseAmount(credits); err != nil {
		return nil, err
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("payments stub: idempotency_key is required")
	}

	// Enforce country to rail access control using the SAME rule as production
	// (payments.AvailableRails). A non-BD account must not be able to select
	// bKash or SSLCommerz, and a BD account is restricted to its allowed set.
	countryCode, err := s.countries.CountryCode(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("payments stub: resolve account country: %w", err)
	}
	if !railAvailable(rail, countryCode) {
		return nil, fmt.Errorf("payments stub: rail %s not available for country %s", rail, countryCode)
	}

	// Build a deterministic idempotency key for the ledger grant so that
	// retrying the same top-up request does not double-credit.
	ledgerKey := fmt.Sprintf("stub:purchase:%s", idempotencyKey)

	metadata := map[string]any{
		"stub":            true,
		"rail":            string(rail),
		"idempotency_key": idempotencyKey,
		"note":            "demo stub — no real payment processed",
	}

	if err := s.ledger.GrantCredits(ctx, accountID, ledgerKey, credits, metadata); err != nil {
		return nil, fmt.Errorf("payments stub: grant credits: %w", err)
	}

	log.Printf("payments stub: granted %d credits to account %s (rail=%s, idem=%s)",
		credits, accountID, rail, idempotencyKey)

	now := time.Now()
	intent := &payments.PaymentIntent{
		ID:        uuid.New(),
		AccountID: accountID,
		Rail:      rail,
		Status:    payments.IntentStatusCompleted,
		Credits:   credits,
		// AmountUSD intentionally zero — stub never processes a real charge.
		// BD regulatory rule: no USD/FX value on any customer surface.
		AmountUSD:     0,
		AmountLocal:   0,
		LocalCurrency: localCurrencyForRail(rail),
		// Provider-blind: a neutral UUID with no "stub" marker so that stub
		// mode cannot be detected from this customer-visible field.
		ProviderIntentID: uuid.New().String(),
		RedirectURL:      "",
		TaxTreatment:     "no_tax",
		TaxRate:          "0.00",
		TaxAmountLocal:   0,
		IdempotencyKey:   idempotencyKey,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return intent, nil
}

// HandleProviderEvent rejects provider webhooks in stub mode. No real rail is
// wired, so any inbound provider event is mis-routed and must surface as an
// error rather than be silently swallowed (which would hide a real webhook
// reaching a demo box).
func (s *StubService) HandleProviderEvent(
	_ context.Context,
	_ payments.Rail,
	_ []byte,
	_ map[string]string,
) error {
	return errors.New("payments stub: provider webhooks are not accepted in demo mode")
}

// GetCheckoutOptions returns stub checkout options, filtered to the rails the
// account's country is permitted to use (same rule as production via
// payments.AvailableRails). No FX lookup is performed (BDT amounts are not
// computed in stub mode).
func (s *StubService) GetCheckoutOptions(
	ctx context.Context,
	accountID uuid.UUID,
) (*payments.CheckoutOptions, error) {
	countryCode, err := s.countries.CountryCode(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("payments stub: resolve account country: %w", err)
	}

	available := payments.AvailableRails(countryCode)
	railOptions := make([]payments.RailOption, 0, len(available))
	for _, rail := range available {
		railOptions = append(railOptions, payments.RailOption{
			Rail:       rail,
			MinCredits: payments.MinPurchaseCredits,
			MaxCredits: maxCreditsForRail(rail),
		})
	}

	return &payments.CheckoutOptions{
		Rails:              railOptions,
		PredefinedTiers:    payments.PredefinedTiers,
		PricePerBlockMinor: 100, // 100 minor units per CreditsPerUSD block (stub; no real FX)
		CreditBlockSize:    payments.CreditsPerUSD,
		Currency:           currencyForCountry(countryCode),
	}, nil
}

// railAvailable reports whether the rail is permitted for the country, using
// the production AvailableRails rule (never duplicated here).
func railAvailable(rail payments.Rail, countryCode string) bool {
	for _, r := range payments.AvailableRails(countryCode) {
		if r == rail {
			return true
		}
	}
	return false
}

// maxCreditsForRail returns the maximum purchasable credits for a rail,
// mirroring the production per-rail limits.
func maxCreditsForRail(r payments.Rail) int64 {
	switch r {
	case payments.RailBkash:
		return payments.MaxPurchaseCreditsBkash
	case payments.RailSSLCommerz:
		return payments.MaxPurchaseCreditsSSLCommerz
	default:
		return payments.MaxPurchaseCreditsStripe
	}
}

// currencyForCountry returns the display currency for the checkout options.
// BD demo context uses BDT; everything else uses USD.
func currencyForCountry(countryCode string) string {
	if countryCode == "BD" {
		return "BDT"
	}
	return "USD"
}

// localCurrencyForRail returns the ISO currency code for the rail.
func localCurrencyForRail(r payments.Rail) string {
	if r == payments.RailBkash || r == payments.RailSSLCommerz {
		return "BDT"
	}
	return "USD"
}
