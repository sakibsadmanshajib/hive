package payments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
	platformhttp "github.com/hivegpt/hive/apps/control-plane/internal/platform/http"
)

// ---------------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------------

type stubPaymentService struct {
	checkoutOptions *payments.CheckoutOptions
	checkoutErr     error

	initiateResult *payments.PaymentIntent
	initiateErr    error

	handleEventErr error

	// recorded args
	lastHandleRail payments.Rail
	lastHandleBody []byte
}

func (s *stubPaymentService) GetCheckoutOptions(_ context.Context, _ uuid.UUID) (*payments.CheckoutOptions, error) {
	return s.checkoutOptions, s.checkoutErr
}

func (s *stubPaymentService) InitiateCheckout(_ context.Context, _ uuid.UUID, _ payments.Rail, _ int64, _, _ string) (*payments.PaymentIntent, error) {
	return s.initiateResult, s.initiateErr
}

func (s *stubPaymentService) HandleProviderEvent(_ context.Context, rail payments.Rail, rawBody []byte, _ map[string]string) error {
	s.lastHandleRail = rail
	s.lastHandleBody = rawBody
	return s.handleEventErr
}

// stubAccountResolver always returns a fixed accountID or an error.
type stubAccountResolver struct {
	accountID uuid.UUID
	err       error
}

func (s *stubAccountResolver) EnsureViewerContext(_ context.Context) (uuid.UUID, error) {
	return s.accountID, s.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newHandler(svc *stubPaymentService, resolver *stubAccountResolver) *payments.Handler {
	return payments.NewHandler(svc, resolver)
}

// ---------------------------------------------------------------------------
// GET /api/v1/accounts/current/checkout/rails
// ---------------------------------------------------------------------------

func TestGetRails_ReturnsAvailableRailsForCountry(t *testing.T) {
	t.Run("US account returns only stripe", func(t *testing.T) {
		svc := &stubPaymentService{
			checkoutOptions: &payments.CheckoutOptions{
				Rails: []payments.RailOption{
					{Rail: payments.RailStripe, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsStripe},
				},
				PredefinedTiers: payments.PredefinedTiers,
			},
		}
		resolver := &stubAccountResolver{accountID: uuid.New()}
		h := newHandler(svc, resolver)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp payments.CheckoutOptions
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Rails) != 1 {
			t.Fatalf("expected 1 rail, got %d", len(resp.Rails))
		}
		if resp.Rails[0].Rail != payments.RailStripe {
			t.Errorf("expected stripe, got %s", resp.Rails[0].Rail)
		}
	})

	t.Run("BD account returns all 3 rails", func(t *testing.T) {
		svc := &stubPaymentService{
			checkoutOptions: &payments.CheckoutOptions{
				Rails: []payments.RailOption{
					{Rail: payments.RailStripe, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsStripe},
					{Rail: payments.RailBkash, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsBkash},
					{Rail: payments.RailSSLCommerz, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsSSLCommerz},
				},
				PredefinedTiers: payments.PredefinedTiers,
			},
		}
		resolver := &stubAccountResolver{accountID: uuid.New()}
		h := newHandler(svc, resolver)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp payments.CheckoutOptions
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Rails) != 3 {
			t.Fatalf("expected 3 rails, got %d", len(resp.Rails))
		}
	})
}

func TestGetRails_IncludesTiersAndLimits(t *testing.T) {
	svc := &stubPaymentService{
		checkoutOptions: &payments.CheckoutOptions{
			Rails: []payments.RailOption{
				{Rail: payments.RailStripe, MinCredits: payments.MinPurchaseCredits, MaxCredits: payments.MaxPurchaseCreditsStripe},
			},
			PredefinedTiers: payments.PredefinedTiers,
		},
	}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp payments.CheckoutOptions
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.PredefinedTiers) == 0 {
		t.Error("expected predefined_tiers to be non-empty")
	}
	if len(resp.Rails) == 0 {
		t.Error("expected rails to be non-empty")
	}
	if resp.Rails[0].MinCredits <= 0 {
		t.Error("expected min_credits to be positive")
	}
	if resp.Rails[0].MaxCredits <= 0 {
		t.Error("expected max_credits to be positive")
	}
}

func TestGetRails_Unauthenticated_Returns401(t *testing.T) {
	svc := &stubPaymentService{}
	resolver := &stubAccountResolver{err: errors.New("unauthenticated")}
	h := newHandler(svc, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/v1/accounts/current/checkout/initiate
// ---------------------------------------------------------------------------

func TestInitiateCheckout_HappyPath(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour)
	intentID := uuid.New()
	svc := &stubPaymentService{
		initiateResult: &payments.PaymentIntent{
			ID:          intentID,
			Rail:        payments.RailStripe,
			Credits:     10000,
			AmountUSD:   10,
			RedirectURL: "https://stripe.com/checkout/abc",
			ExpiresAt:   &expiresAt,
			TaxTreatment: "no_tax",
		},
	}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	body := `{"rail":"stripe","credits":10000,"idempotency_key":"test-key-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["payment_intent_id"]; !ok {
		t.Error("expected payment_intent_id in response")
	}
	if _, ok := resp["redirect_url"]; !ok {
		t.Error("expected redirect_url in response")
	}
}

func TestInitiateCheckout_MissingBillingProfile(t *testing.T) {
	svc := &stubPaymentService{
		initiateErr: payments.ErrBillingProfileRequired,
	}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	body := `{"rail":"stripe","credits":10000,"idempotency_key":"test-key-456"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got: %v", resp["error"])
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "billing profile") {
		t.Errorf("expected billing profile message, got: %s", msg)
	}
}

func TestInitiateCheckout_InvalidCredits(t *testing.T) {
	svc := &stubPaymentService{}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	body := `{"rail":"stripe","credits":500,"idempotency_key":"test-key-789"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestInitiateCheckout_MissingIdempotencyKey(t *testing.T) {
	svc := &stubPaymentService{}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	body := `{"rail":"stripe","credits":10000,"idempotency_key":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestInitiateCheckout_MissingRail(t *testing.T) {
	svc := &stubPaymentService{}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	body := `{"rail":"","credits":10000,"idempotency_key":"test-key-abc"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Webhook tests
// ---------------------------------------------------------------------------

func TestWebhook_ReadsRawBodyFirst(t *testing.T) {
	svc := &stubPaymentService{}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	bodyContent := []byte(`{"type":"payment_intent.succeeded","data":{"object":{"id":"pi_abc"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(bodyContent))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Always 200 for webhooks
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	// Body was passed to the service
	if len(svc.lastHandleBody) == 0 {
		t.Error("expected raw body to be passed to HandleProviderEvent")
	}
	if string(svc.lastHandleBody) != string(bodyContent) {
		t.Errorf("expected body %q, got %q", bodyContent, svc.lastHandleBody)
	}
}

func TestWebhook_AlwaysReturns200(t *testing.T) {
	svc := &stubPaymentService{
		handleEventErr: errors.New("provider processing error"),
	}
	resolver := &stubAccountResolver{accountID: uuid.New()}
	h := newHandler(svc, resolver)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(`{"event":"test"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Must return 200 even when service returns error
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even on error, got %d", rr.Code)
	}
}

func TestWebhook_RoutesCorrectRail(t *testing.T) {
	tests := []struct {
		path         string
		expectedRail payments.Rail
	}{
		{"/webhooks/stripe", payments.RailStripe},
		{"/webhooks/bkash/callback", payments.RailBkash},
		{"/webhooks/sslcommerz/ipn", payments.RailSSLCommerz},
	}

	for _, tt := range tests {
		t.Run(string(tt.expectedRail), func(t *testing.T) {
			svc := &stubPaymentService{}
			resolver := &stubAccountResolver{accountID: uuid.New()}
			h := newHandler(svc, resolver)

			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(`{"event":"test"}`))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
			if svc.lastHandleRail != tt.expectedRail {
				t.Errorf("expected rail %s, got %s", tt.expectedRail, svc.lastHandleRail)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Router integration tests
// ---------------------------------------------------------------------------

// stubHTTPPaymentsHandler is a minimal http.Handler for router integration tests.
// It responds 200 to all requests so we can test auth wiring.
type stubHTTPPaymentsHandler struct{}

func (s *stubHTTPPaymentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}


func TestRouterIntegration_WebhookStripe_NoAuth_Returns200(t *testing.T) {
	// Create a test auth server that rejects all tokens
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer authServer.Close()

	authClient := auth.NewClient(authServer.URL, "test-anon-key")
	authMiddleware := auth.NewMiddleware(authClient)

	stubSvc := &stubPaymentService{}
	stubResolver := &stubAccountResolver{accountID: uuid.New()}
	paymentsHandler := payments.NewHandler(stubSvc, stubResolver)

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:  authMiddleware,
		PaymentsHandler: paymentsHandler,
	})

	// POST /webhooks/stripe without any auth header — must return 200
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(`{"event":"test"}`))
	// Explicitly NO Authorization header
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for unauthenticated webhook, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRouterIntegration_CheckoutInitiate_NoAuth_Returns401(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer authServer.Close()

	authClient := auth.NewClient(authServer.URL, "test-anon-key")
	authMiddleware := auth.NewMiddleware(authClient)

	stubSvc := &stubPaymentService{}
	stubResolver := &stubAccountResolver{accountID: uuid.New()}
	paymentsHandler := payments.NewHandler(stubSvc, stubResolver)

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:  authMiddleware,
		PaymentsHandler: paymentsHandler,
	})

	// POST /api/v1/accounts/current/checkout/initiate without auth — must return 401
	body := `{"rail":"stripe","credits":10000,"idempotency_key":"test-key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", strings.NewReader(body))
	// Explicitly NO Authorization header
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated checkout initiate, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRouterIntegration_CheckoutRails_NoAuth_Returns401(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer authServer.Close()

	authClient := auth.NewClient(authServer.URL, "test-anon-key")
	authMiddleware := auth.NewMiddleware(authClient)

	stubSvc := &stubPaymentService{}
	stubResolver := &stubAccountResolver{accountID: uuid.New()}
	paymentsHandler := payments.NewHandler(stubSvc, stubResolver)

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:  authMiddleware,
		PaymentsHandler: paymentsHandler,
	})

	// GET /api/v1/accounts/current/checkout/rails without auth — must return 401
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
	// Explicitly NO Authorization header
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated checkout rails, got %d body=%s", rr.Code, rr.Body.String())
	}
}
