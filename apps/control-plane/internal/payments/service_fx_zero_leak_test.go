package payments_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
)

// bannedFXKeys is the canonical list of customer-surface keys forbidden by
// Phase 17 (FX/USD Zero-Leak). Internal accounting USD persists in DB +
// server→Stripe payload; it MUST NOT appear on any customer-visible JSON.
var bannedFXKeys = []string{
	"amount_usd",
	"usd_",
	"fx_",
	"price_per_credit_usd",
	"exchange_rate",
}

// FX-17-01 RED: payments.PaymentIntent currently has
// `AmountUSD int64 \`json:"amount_usd"\`` (types.go:74). Whenever the type
// is marshaled — directly or via embedding — the customer would see
// `amount_usd`. Task 2 changes that tag to `json:"-"` (or splits a wire DTO).
func TestPaymentIntentWireShape_FXZeroLeak(t *testing.T) {
	now := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	intent := payments.PaymentIntent{
		ID:               uuid.New(),
		AccountID:        uuid.New(),
		Rail:             payments.RailStripe,
		Status:           payments.IntentStatusCreated,
		Credits:          100_000,
		AmountUSD:        100,
		AmountLocal:      12_500_00,
		LocalCurrency:    "BDT",
		ProviderIntentID: "pi_test",
		RedirectURL:      "https://pay.example.test",
		TaxTreatment:     "vat_inclusive",
		TaxRate:          "0.15",
		TaxAmountLocal:   1_875_00,
		IdempotencyKey:   "idem-key-1",
		Metadata:         map[string]any{"test": true},
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal PaymentIntent: %v", err)
	}

	for _, key := range bannedFXKeys {
		key := key
		t.Run("no_"+key, func(t *testing.T) {
			if bytes.Contains(raw, []byte(key)) {
				t.Errorf("PaymentIntent JSON wire shape contains banned key %q\npayload: %s", key, raw)
			}
		})
	}
}

// FX-17-03 RED: payments.CheckoutOptions currently exposes
// `PricePerCreditUSD float64 \`json:"price_per_credit_usd"\``
// (service.go around CheckoutOptions). Task 4 replaces with
// `PricePerCreditMinor int64` + `Currency string`.
//
// NOTE: at b87fa24 the struct does not yet carry PricePerCreditUSD —
// audit found the leak inferred from web-console client.ts. We assert the
// wire shape regardless: any future regression that adds the USD key will
// flip this subtest red. Today the test FAILS only if the field exists.
// To make this RED-on-b87fa24, we additionally marshal a synthetic struct
// that mirrors the CURRENT documented public shape per PLAN Task 4.
func TestCheckoutOptionsWireShape_FXZeroLeak(t *testing.T) {
	opts := payments.CheckoutOptions{
		Rails: []payments.RailOption{
			{Rail: payments.RailStripe, MinCredits: 1_000, MaxCredits: 10_000_000},
			{Rail: payments.RailBkash, MinCredits: 1_000, MaxCredits: 30_000_000},
		},
		PredefinedTiers: []int64{1_000, 5_000, 10_000, 50_000, 100_000},
	}

	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal CheckoutOptions: %v", err)
	}

	// Subtest 1: live struct must already be clean.
	for _, key := range bannedFXKeys {
		key := key
		t.Run("live_no_"+key, func(t *testing.T) {
			if bytes.Contains(raw, []byte(key)) {
				t.Errorf("CheckoutOptions JSON wire shape contains banned key %q\npayload: %s", key, raw)
			}
		})
	}

	// Subtest 2: assert per-country primitive landed (Task 4 acceptance).
	// At b87fa24 the struct has no `currency` or `price_per_credit_minor`
	// field, so this subtest FAILS RED today. Task 4 turns it GREEN by
	// adding those fields.
	t.Run("has_per_country_primitive", func(t *testing.T) {
		if !bytes.Contains(raw, []byte(`"currency"`)) {
			t.Errorf("CheckoutOptions wire shape missing required key \"currency\"\npayload: %s", raw)
		}
		if !bytes.Contains(raw, []byte(`"price_per_credit_minor"`)) {
			t.Errorf("CheckoutOptions wire shape missing required key \"price_per_credit_minor\"\npayload: %s", raw)
		}
	})
}
