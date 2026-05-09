package payments_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
)

// =============================================================================
// FX-17-08 — End-to-end customer-USD zero-leak integration test.
//
// Drives the payments HTTP Handler in-process (httptest) for both BD and
// non-BD account fixtures. Asserts:
//   1. Response bytes contain NONE of the FX-tripwire keys.
//   2. Wire shape carries the per-country pricing primitive
//      (`price_per_credit_minor` int + `currency` string).
//   3. BD account → Currency == "BDT".
//   4. Non-BD account → Currency == "USD".
//
// In-process Handler dispatch matches the production routing and exercises
// `writePaymentJSON` directly, so the exact bytes a real customer would
// receive over the wire are what we assert against.
//
// We stub the PaymentService at the boundary the Handler uses (`PaymentService`
// interface). The service-layer per-country resolver is unit-tested separately
// in service_checkout_options_test.go; here we lock the wire-shape contract
// end-to-end through the Handler.
// =============================================================================

// fxBannedKeys must NEVER appear in the response body customers see for any
// checkout-related endpoint. Mirrors `lint-no-customer-usd.mjs` and the
// service-layer regulatory contract.
var fxBannedKeys = []string{
	"amount_usd",
	"usd_",
	"fx_",
	"price_per_credit_usd",
	"exchange_rate",
}

func TestIntegration_GetRails_BDAccount_NoUSDLeak(t *testing.T) {
	t.Parallel()

	svc := &stubPaymentService{
		checkoutOptions: &payments.CheckoutOptions{
			Rails: []payments.RailOption{
				{Rail: payments.RailStripe, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsStripe},
				{Rail: payments.RailBkash, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsBkash},
				{Rail: payments.RailSSLCommerz, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsSSLCommerz},
			},
			PredefinedTiers:     payments.PredefinedTiers,
			PricePerCreditMinor: 12_000, // 120 BDT per CreditsPerUSD block, in paisa
			Currency:            "BDT",
		},
	}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := payments.NewHandler(svc, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	raw := rr.Body.Bytes()

	// ---- Banned-key sweep on the raw wire bytes ----
	for _, key := range fxBannedKeys {
		key := key
		t.Run("no_"+key, func(t *testing.T) {
			t.Parallel()
			if bytes.Contains(raw, []byte(key)) {
				t.Errorf("BD checkout rails wire bytes leak banned key %q\npayload: %s", key, raw)
			}
		})
	}

	// ---- Required positive-shape: BDT primitive present ----
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	priceMinor, ok := decoded["price_per_credit_minor"].(float64) // JSON numbers
	if !ok {
		t.Fatalf("price_per_credit_minor missing or not a number: %v", decoded["price_per_credit_minor"])
	}
	if int64(priceMinor) <= 0 {
		t.Errorf("expected positive price_per_credit_minor, got %v", priceMinor)
	}
	if currency, _ := decoded["currency"].(string); currency != "BDT" {
		t.Errorf("expected currency=BDT for BD account, got %q", currency)
	}
}

func TestIntegration_GetRails_NonBDAccount_NoUSDLeak(t *testing.T) {
	t.Parallel()

	svc := &stubPaymentService{
		checkoutOptions: &payments.CheckoutOptions{
			Rails: []payments.RailOption{
				{Rail: payments.RailStripe, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsStripe},
			},
			PredefinedTiers:     payments.PredefinedTiers,
			PricePerCreditMinor: 100, // 100 cents = $1 per CreditsPerUSD block
			Currency:            "USD",
		},
	}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := payments.NewHandler(svc, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	raw := rr.Body.Bytes()

	// Banned-key sweep — `usd_` substring is forbidden in JSON KEY position
	// (e.g. `amount_usd`, `usd_cents`). The currency *value* "USD" is allowed
	// here because non-BD accounts do see USD as their billing currency; that
	// is structurally distinct from leaking USD as a JSON key on a BD-eligible
	// surface. We assert key-position leaks only.
	keyPosBanned := []string{
		`"amount_usd"`,
		`"price_per_credit_usd"`,
		`"exchange_rate"`,
	}
	for _, key := range keyPosBanned {
		key := key
		t.Run("no_key_"+key, func(t *testing.T) {
			t.Parallel()
			if bytes.Contains(raw, []byte(key)) {
				t.Errorf("non-BD checkout rails wire bytes leak banned JSON key %s\npayload: %s", key, raw)
			}
		})
	}

	// Positive-shape: USD currency + non-zero minor units.
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if currency, _ := decoded["currency"].(string); currency != "USD" {
		t.Errorf("expected currency=USD for non-BD account, got %q", currency)
	}
	priceMinor, ok := decoded["price_per_credit_minor"].(float64)
	if !ok || int64(priceMinor) <= 0 {
		t.Errorf("expected positive price_per_credit_minor, got %v", decoded["price_per_credit_minor"])
	}
}

// TestIntegration_InitiateCheckout_BDAccount_NoUSDLeak drives the second
// customer-surface payments endpoint end-to-end. The initiate response
// carries amount_local + local_currency + payment_intent_id; it must NOT
// carry any USD-denominated field.
func TestIntegration_InitiateCheckout_BDAccount_NoUSDLeak(t *testing.T) {
	t.Parallel()

	intentID := uuid.New()
	svc := &stubPaymentService{
		initiateResult: &payments.PaymentIntent{
			ID:            intentID,
			Rail:          payments.RailBkash,
			Credits:       100_000,
			AmountUSD:     100, // internal-only — must be stripped on wire
			AmountLocal:   12_000_00,
			LocalCurrency: "BDT",
			RedirectURL:   "https://pay.bkash.test/redirect",
			TaxTreatment:  "vat_inclusive",
		},
	}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := payments.NewHandler(svc, resolver)

	body := `{"rail":"bkash","credits":100000,"idempotency_key":"fx-zero-leak-test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	raw := rr.Body.Bytes()
	for _, key := range fxBannedKeys {
		key := key
		t.Run("no_"+key, func(t *testing.T) {
			t.Parallel()
			if bytes.Contains(raw, []byte(key)) {
				t.Errorf("BD initiate response leaks banned key %q\npayload: %s", key, raw)
			}
		})
	}

	// Positive-shape: BDT local primitives must be present.
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if lc, _ := decoded["local_currency"].(string); lc != "BDT" {
		t.Errorf("expected local_currency=BDT, got %q", lc)
	}
	if amt, _ := decoded["amount_local"].(float64); amt <= 0 {
		t.Errorf("expected positive amount_local, got %v", amt)
	}
}
