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
//   - All credits flow through the same LedgerGranter interface used by the
//     real service, so the ledger remains append-only and the balance is correct.
//   - math/big is used for all credit arithmetic to satisfy the project-wide
//     float-free financial computation invariant.
//   - Amount fields returned in stub responses contain the credit count only;
//     no USD amount, no FX rate, no amount_local is derived (there is nothing
//     to convert without a real FX snapshot).
//   - BD regulatory rule: amount_usd is NOT included in any customer-facing
//     response (same constraint as the production handler — enforced by the
//     initiateResponse DTO in payments/http.go).
//
// This package MUST NOT be imported in production builds unless HIVE_PAYMENTS_STUB
// is explicitly set. Callers should use IsEnabled() to guard the import.
package stub

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// envFlag is the environment variable that enables demo stub mode.
const envFlag = "HIVE_PAYMENTS_STUB"

// IsEnabled reports whether HIVE_PAYMENTS_STUB=true is set.
// Call this in main.go before constructing the payment service so that the
// decision is made once at startup and logged clearly.
func IsEnabled() bool {
	return strings.TrimSpace(os.Getenv(envFlag)) == "true"
}

// ---------------------------------------------------------------------------
// StubService
// ---------------------------------------------------------------------------

// StubService implements the payments.PaymentService interface.
// It replaces InitiateCheckout with an immediate ledger grant and returns
// a synthetic success response, bypassing all payment rails.
type StubService struct {
	ledger LedgerGranter
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
func NewStubService(ledger LedgerGranter) *StubService {
	log.Printf(
		"[WARN] payments: DEMO STUB MODE ACTIVE (env %s=true) — " +
			"real payment rails are disabled; credits granted instantly. " +
			"Do NOT use in production.",
		envFlag,
	)
	return &StubService{ledger: ledger}
}

// ---------------------------------------------------------------------------
// payments.PaymentService implementation
// ---------------------------------------------------------------------------

// InitiateCheckout validates the request, immediately grants credits through
// the real ledger, and returns a synthetic PaymentIntent in the 'completed'
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

	// Use math/big for credit arithmetic to satisfy the float-free mandate,
	// even though the stub only does integer operations. This keeps the
	// invariant self-consistent if the calculation ever grows more complex.
	creditsBig := new(big.Int).SetInt64(credits)
	if creditsBig.Sign() <= 0 {
		return nil, fmt.Errorf("payments stub: credits must be positive")
	}

	// Build a deterministic idempotency key for the ledger grant so that
	// retrying the same top-up request does not double-credit.
	ledgerKey := fmt.Sprintf("stub:purchase:%s", idempotencyKey)

	metadata := map[string]any{
		"stub":          true,
		"rail":          string(rail),
		"idempotency_key": idempotencyKey,
		"note":          "demo stub — no real payment processed",
	}

	if err := s.ledger.GrantCredits(ctx, accountID, ledgerKey, credits, metadata); err != nil {
		return nil, fmt.Errorf("payments stub: grant credits: %w", err)
	}

	log.Printf("payments stub: granted %d credits to account %s (rail=%s, idem=%s)",
		credits, accountID, rail, idempotencyKey)

	now := time.Now()
	intent := &payments.PaymentIntent{
		ID:               uuid.New(),
		AccountID:        accountID,
		Rail:             rail,
		Status:           payments.IntentStatusCompleted,
		Credits:          credits,
		// AmountUSD intentionally zero — stub never processes a real charge.
		// BD regulatory rule: no USD/FX value on any customer surface.
		AmountUSD:        0,
		AmountLocal:      0,
		LocalCurrency:    localCurrencyForRail(rail),
		ProviderIntentID: fmt.Sprintf("stub_%s", uuid.New().String()),
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

// HandleProviderEvent is a no-op in stub mode — no provider sends webhooks.
func (s *StubService) HandleProviderEvent(
	_ context.Context,
	_ payments.Rail,
	_ []byte,
	_ map[string]string,
) error {
	return nil
}

// GetCheckoutOptions returns stub checkout options.
// Rails available mirror the production AvailableRails logic but with
// no FX lookup required (BDT amounts are not computed in stub mode).
func (s *StubService) GetCheckoutOptions(
	_ context.Context,
	_ uuid.UUID,
) (*payments.CheckoutOptions, error) {
	return &payments.CheckoutOptions{
		Rails: []payments.RailOption{
			{Rail: payments.RailStripe, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsStripe},
			{Rail: payments.RailBkash, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsBkash},
			{Rail: payments.RailSSLCommerz, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsSSLCommerz},
		},
		PredefinedTiers:    payments.PredefinedTiers,
		PricePerBlockMinor: 100, // 100 units per CreditsPerUSD block (stub; no real FX)
		CreditBlockSize:    payments.CreditsPerUSD,
		Currency:           "BDT", // Stub targets BD demo context
	}, nil
}

// localCurrencyForRail returns the ISO currency code for the rail.
func localCurrencyForRail(r payments.Rail) string {
	if r == payments.RailBkash || r == payments.RailSSLCommerz {
		return "BDT"
	}
	return "USD"
}
