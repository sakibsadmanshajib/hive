package payments_test

// ---------------------------------------------------------------------------
// BD Regulatory Regression Guard — Phase 17 (FX-17-01)
//
// REGULATORY RULE (NEVER DELETE THIS TEST):
//   BD customers (Bangladesh) MUST NEVER see amount_usd, fx_rate,
//   exchange_rate, or any FX/USD language in checkout HTTP responses.
//   This is a P0 compliance requirement enforced since Phase 17.
//   Reference: apps/control-plane/internal/payments/http.go (initiateResponse)
//   and CLAUDE.md "Regulatory Rules" section.
//
// These tests assert the wire DTO JSON emitted by the checkout endpoints
// contains no USD or FX fields, regardless of what the internal
// PaymentIntent struct carries. They are intentionally verbose so any
// reviewer who stumbles on them understands why they must not be weakened.
// ---------------------------------------------------------------------------

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// forbiddenCheckoutKeys lists every JSON key name that must NEVER appear in a
// BD customer checkout HTTP response. These are matched as exact JSON key
// strings (i.e. `"key":` in the raw body), not as substrings of values.
// Extend this slice if new regulated fields are added.
// DO NOT REMOVE existing entries without a compliance sign-off.
var forbiddenCheckoutKeys = []string{
	"amount_usd",
	"fx_rate",
	"exchange_rate",
	"fx_snapshot_id",
	"usd_amount",
}

// assertNoForbiddenFields fails the test if the JSON body contains any of the
// forbidden key strings as a JSON key (not as a value).
// Strategy: check for the pattern `"key":` in the lowercased raw JSON, which
// matches keys but not string values. Also do an exact top-level key check via
// map unmarshalling for defence-in-depth on nested objects.
func assertNoForbiddenFields(t *testing.T, body []byte) {
	t.Helper()

	lower := strings.ToLower(string(body))
	for _, forbidden := range forbiddenCheckoutKeys {
		// Match `"forbidden":` — this is a JSON key pattern, not a value.
		keyPattern := `"` + forbidden + `":`
		if strings.Contains(lower, keyPattern) {
			t.Errorf("BD regulatory violation: response JSON contains forbidden key %q; body: %s", forbidden, body)
		}
	}

	// Exact top-level key check via map decode (catches keys without colon match too).
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		// Not a JSON object (e.g. error body) — skip map check.
		return
	}
	for _, forbidden := range forbiddenCheckoutKeys {
		if _, ok := m[forbidden]; ok {
			t.Errorf("BD regulatory violation: top-level key %q present in response; body: %s", forbidden, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: POST /api/v1/accounts/current/checkout/initiate (BD rail)
// ---------------------------------------------------------------------------

// TestBDCheckoutOmitsUSDAmounts is the primary BD regulatory regression guard.
//
// REGULATORY RULE: BD customers must NEVER see amount_usd or FX language.
// Phase 17 (FX-17-01) introduced initiateResponse DTO that deliberately omits
// AmountUSD. This test ensures that wire shape contract is never broken by
// a future refactor that accidentally promotes the internal AmountUSD field.
func TestBDCheckoutOmitsUSDAmounts(t *testing.T) {
	accountID := uuid.New()

	// The internal PaymentIntent intentionally carries AmountUSD (used by
	// Stripe rail server-side). It must NEVER be serialised onto the wire DTO.
	now := time.Now().UTC()
	intent := &payments.PaymentIntent{
		ID:            uuid.New(),
		AccountID:     accountID,
		Rail:          payments.RailBkash,
		Status:        payments.IntentStatusPendingRedirect,
		Credits:       10_000,
		AmountUSD:     100,   // internal accounting — MUST NOT appear in response
		AmountLocal:   11_500, // BDT paisa
		LocalCurrency: "BDT",
		TaxTreatment:  "inclusive",
		RedirectURL:   "https://pay.bkash.example/redirect",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	svc := &stubPaymentService{initiateResult: intent}
	accts := &stubAccountResolver{accountID: accountID}
	handler := payments.NewHandler(svc, accts)

	body := `{"rail":"bkash","credits":10000,"idempotency_key":"idem-bd-001"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d; body: %s", rr.Code, rr.Body.String())
	}

	rawBody := rr.Body.Bytes()
	assertNoForbiddenFields(t, rawBody)

	// Positive assertion: the response must contain expected BD fields.
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v; body: %s", err, rawBody)
	}
	for _, required := range []string{"payment_intent_id", "rail", "credits", "amount_local", "local_currency"} {
		if _, ok := resp[required]; !ok {
			t.Errorf("expected field %q to be present in BD checkout response", required)
		}
	}

	// Verify local_currency is BDT (not USD) for a BD rail response.
	var currency string
	if err := json.Unmarshal(resp["local_currency"], &currency); err != nil {
		t.Fatalf("could not decode local_currency: %v", err)
	}
	if currency != "BDT" {
		t.Errorf("expected local_currency=BDT for BD checkout, got %q", currency)
	}
}

// TestBDCheckoutOmitsUSDAmounts_SSLCommerz repeats the assertion for the
// SSLCommerz rail (the other BD-exclusive rail).
func TestBDCheckoutOmitsUSDAmounts_SSLCommerz(t *testing.T) {
	accountID := uuid.New()
	now := time.Now().UTC()
	intent := &payments.PaymentIntent{
		ID:            uuid.New(),
		AccountID:     accountID,
		Rail:          payments.RailSSLCommerz,
		Status:        payments.IntentStatusPendingRedirect,
		Credits:       50_000,
		AmountUSD:     500,  // internal only
		AmountLocal:   57_500,
		LocalCurrency: "BDT",
		TaxTreatment:  "inclusive",
		RedirectURL:   "https://pay.sslcommerz.example/redirect",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	svc := &stubPaymentService{initiateResult: intent}
	accts := &stubAccountResolver{accountID: accountID}
	handler := payments.NewHandler(svc, accts)

	body := `{"rail":"sslcommerz","credits":50000,"idempotency_key":"idem-bd-002"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d; body: %s", rr.Code, rr.Body.String())
	}
	assertNoForbiddenFields(t, rr.Body.Bytes())
}

// TestNonBDCheckoutOmitsUSDAmounts verifies that non-BD (Stripe/USD) checkout
// responses also never expose the internal AmountUSD field. Even for non-BD
// customers the initiateResponse DTO must not leak internal accounting fields.
//
// Note: non-BD customers legitimately receive local_currency="USD" and
// amount_local in USD cents, but amount_usd as a separate field is never
// part of the wire contract.
func TestNonBDCheckoutOmitsUSDAmounts(t *testing.T) {
	accountID := uuid.New()
	now := time.Now().UTC()
	intent := &payments.PaymentIntent{
		ID:            uuid.New(),
		AccountID:     accountID,
		Rail:          payments.RailStripe,
		Status:        payments.IntentStatusPendingRedirect,
		Credits:       10_000,
		AmountUSD:     100,  // internal accounting only
		AmountLocal:   100,  // USD cents (non-BD)
		LocalCurrency: "USD",
		TaxTreatment:  "reverse_charge",
		RedirectURL:   "https://pay.stripe.example/redirect",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	svc := &stubPaymentService{initiateResult: intent}
	accts := &stubAccountResolver{accountID: accountID}
	handler := payments.NewHandler(svc, accts)

	body := `{"rail":"stripe","credits":10000,"idempotency_key":"idem-nonbd-001"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d; body: %s", rr.Code, rr.Body.String())
	}
	assertNoForbiddenFields(t, rr.Body.Bytes())

	// Stripe response may legitimately carry local_currency=USD.
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	// But amount_usd must still be absent.
	if _, ok := resp["amount_usd"]; ok {
		t.Error("amount_usd must not be present even for non-BD Stripe checkout response")
	}
}

// TestGetCheckoutOptions_BDResponse_OmitsFXRate verifies that the GET rails
// endpoint response for a BD account does not expose FX rates or USD amounts.
// CheckoutOptions only returns pricing primitive scalars (price_per_block_minor
// in BDT paisa) without any exchange rate hint.
func TestGetCheckoutOptions_BDResponse_OmitsFXRate(t *testing.T) {
	accountID := uuid.New()

	bdOpts := &payments.CheckoutOptions{
		Rails: []payments.RailOption{
			{Rail: payments.RailBkash, MinCredits: 1_000, MaxCredits: 30_000_000},
			{Rail: payments.RailSSLCommerz, MinCredits: 1_000, MaxCredits: 500_000_000},
			{Rail: payments.RailStripe, MinCredits: 1_000, MaxCredits: 10_000_000},
		},
		PredefinedTiers:    []int64{1_000, 5_000, 10_000, 50_000, 100_000},
		PricePerBlockMinor: 11500, // BDT paisa per 100k credits (no FX rate exposed)
		CreditBlockSize:    100_000,
		Currency:           "BDT",
	}

	svc := &stubPaymentService{checkoutOptions: bdOpts}
	accts := &stubAccountResolver{accountID: accountID}
	handler := payments.NewHandler(svc, accts)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d; body: %s", rr.Code, rr.Body.String())
	}
	assertNoForbiddenFields(t, rr.Body.Bytes())
}

// TestBDCheckoutOmitsUSDAmounts_ErrorResponseClean verifies that even error
// responses on the BD checkout path do not leak FX or USD language.
// Phase 17 FX-17 review-pass replaced raw error forwarding with classified
// opaque messages.
func TestBDCheckoutOmitsUSDAmounts_ErrorResponseClean(t *testing.T) {
	accountID := uuid.New()

	svc := &stubPaymentService{
		// Simulate an internal FX error that would have previously leaked
		// "payments: invalid effective rate 115.500000" onto the wire.
		initiateErr: payments.ErrFXUnavailable,
	}
	accts := &stubAccountResolver{accountID: accountID}
	handler := payments.NewHandler(svc, accts)

	body := `{"rail":"bkash","credits":10000,"idempotency_key":"idem-bd-err-001"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Must not be 200 — but more importantly must not contain FX/USD language.
	rawBody := rr.Body.Bytes()
	assertNoForbiddenFields(t, rawBody)

	// The error message must not mention "rate", "FX", "USD", or numeric
	// exchange-rate values that could reveal internal FX data.
	lower := strings.ToLower(string(rawBody))
	for _, leakyPhrase := range []string{"rate", "115.", "fx rate", "exchange"} {
		if strings.Contains(lower, leakyPhrase) {
			t.Errorf("BD error response leaks FX/USD language %q; body: %s", leakyPhrase, rawBody)
		}
	}
}

// stubBDCheckoutAccountResolver returns a fixed accountID.
// Reuses stubAccountResolver from http_test.go (same package_test scope).

// stubBDInitiateCtx captures the context to verify no cross-contamination
// between BD and non-BD response shapes.
type stubBDInitiateCtx struct {
	result *payments.PaymentIntent
	err    error
}

func (s *stubBDInitiateCtx) GetCheckoutOptions(_ context.Context, _ uuid.UUID) (*payments.CheckoutOptions, error) {
	return nil, nil
}

func (s *stubBDInitiateCtx) InitiateCheckout(_ context.Context, _ uuid.UUID, _ payments.Rail, _ int64, _, _ string) (*payments.PaymentIntent, error) {
	return s.result, s.err
}

func (s *stubBDInitiateCtx) HandleProviderEvent(_ context.Context, _ payments.Rail, _ []byte, _ map[string]string) error {
	return nil
}
