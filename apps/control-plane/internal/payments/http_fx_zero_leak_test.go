package payments

import (
	"bytes"
	"encoding/json"
	"testing"
)

// FX-17-01 RED: customer-surface JSON of POST /v1/payments/intents response
// must contain none of the banned USD/FX keys.
//
// Phase 17 — wire-shape contract lock. This test is INTENTIONALLY failing on
// b87fa24 because `initiateResponse` still carries `AmountUSD int64
// `json:"amount_usd"`` (apps/control-plane/internal/payments/http.go:119).
// Task 2 turns this GREEN by removing that field.
//
// Lives in `package payments` (internal) because `initiateResponse` is an
// unexported type. The test asserts the wire bytes the HTTP handler emits.
func TestInitiateResponseWireShape_FXZeroLeak(t *testing.T) {
	expires := "2026-05-08T12:34:56Z"
	// Note: post-FX-17-01 GREEN, initiateResponse no longer carries an
	// AmountUSD field. Internal accounting USD persists on
	// payments.PaymentIntent (json:"-") and the server→Stripe payload is
	// built from the Go struct, not from this wire DTO.
	resp := initiateResponse{
		PaymentIntentID: "11111111-2222-3333-4444-555555555555",
		RedirectURL:     "https://pay.example.test/redirect",
		Rail:            RailBkash,
		Credits:         100_000,
		AmountLocal:     12_500_00,
		LocalCurrency:   "BDT",
		TaxTreatment:    "vat_inclusive",
		ExpiresAt:       &expires,
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal initiateResponse: %v", err)
	}

	bannedKeys := []string{
		"amount_usd",
		"usd_",
		"fx_",
		"price_per_credit_usd",
		"exchange_rate",
	}

	for _, key := range bannedKeys {
		key := key
		t.Run("no_"+key, func(t *testing.T) {
			if bytes.Contains(raw, []byte(key)) {
				t.Errorf("initiateResponse JSON wire shape contains banned key %q\npayload: %s", key, raw)
			}
		})
	}
}
