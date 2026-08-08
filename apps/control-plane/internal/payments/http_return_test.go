package payments_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	platformhttp "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/http"
)

// ---------------------------------------------------------------------------
// The browser return routes are gone from the webhook surface (issue #538)
// ---------------------------------------------------------------------------

func TestBrowserReturnPathsAreNotWebhookRoutes(t *testing.T) {
	for _, path := range []string{
		"/webhooks/sslcommerz/success",
		"/webhooks/sslcommerz/fail",
		"/webhooks/sslcommerz/cancel",
	} {
		t.Run(path, func(t *testing.T) {
			svc := &stubPaymentService{}
			h := newHandler(svc, &stubAccountResolver{accountID: uuid.New()})

			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("val_id=abc&sessionkey=sk"))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Errorf("expected 404 for a retired browser return route, got %d", rr.Code)
			}
			if svc.handleEventCalls != 0 {
				t.Errorf("a browser return must never reach settlement, got %d event calls", svc.handleEventCalls)
			}
		})
	}
}

func TestRouterNoLongerRegistersBrowserReturnRoutes(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authServer.Close()

	svc := &stubPaymentService{}
	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:  auth.NewMiddleware(auth.NewClient(authServer.URL, "test-anon-key")),
		PaymentsHandler: payments.NewHandler(svc, &stubAccountResolver{accountID: uuid.New()}),
	})

	for _, path := range []string{
		"/webhooks/sslcommerz/success",
		"/webhooks/sslcommerz/fail",
		"/webhooks/sslcommerz/cancel",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("val_id=abc"))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404 from the router, got %d", path, rr.Code)
		}
	}
	if svc.handleEventCalls != 0 {
		t.Errorf("expected zero settlement calls, got %d", svc.handleEventCalls)
	}
}

func TestSettlementStillDrivenByTheIPNWebhook(t *testing.T) {
	svc := &stubPaymentService{}
	h := newHandler(svc, &stubAccountResolver{accountID: uuid.New()})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/sslcommerz/ipn", strings.NewReader("val_id=abc&sessionkey=sk"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from the IPN webhook, got %d", rr.Code)
	}
	if svc.handleEventCalls != 1 {
		t.Fatalf("expected the IPN webhook to drive settlement exactly once, got %d", svc.handleEventCalls)
	}
	if svc.lastHandleRail != payments.RailSSLCommerz {
		t.Errorf("expected rail sslcommerz, got %s", svc.lastHandleRail)
	}
}

// ---------------------------------------------------------------------------
// GET /api/v1/accounts/current/checkout/intent
// ---------------------------------------------------------------------------

const intentPath = "/api/v1/accounts/current/checkout/intent"

func TestGetCheckoutIntent_ReturnsAuthoritativeState(t *testing.T) {
	intentID := uuid.New()
	accountID := uuid.New()
	svc := &stubPaymentService{intentView: &payments.CheckoutIntentView{
		PaymentIntentID: intentID.String(),
		Rail:            payments.RailSSLCommerz,
		Status:          payments.IntentStatusCompleted,
		State:           payments.ReturnStateSuccess,
		Credits:         5000,
	}}
	h := newHandler(svc, &stubAccountResolver{accountID: accountID})

	req := httptest.NewRequest(http.MethodGet, intentPath+"?payment_intent_id="+intentID.String(), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.lastIntentAccount != accountID {
		t.Errorf("expected the viewer account to scope the lookup, got %s", svc.lastIntentAccount)
	}
	if svc.lastIntentID != intentID {
		t.Errorf("expected intent %s, got %s", intentID, svc.lastIntentID)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["state"] != string(payments.ReturnStateSuccess) {
		t.Errorf("expected state success, got %v", body["state"])
	}
}

func TestGetCheckoutIntent_Unauthenticated_Returns401(t *testing.T) {
	svc := &stubPaymentService{}
	h := newHandler(svc, &stubAccountResolver{err: errors.New("unauthenticated")})

	req := httptest.NewRequest(http.MethodGet, intentPath+"?payment_intent_id="+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if svc.intentCalls != 0 {
		t.Errorf("expected no service call for an unauthenticated read, got %d", svc.intentCalls)
	}
}

func TestGetCheckoutIntent_ForeignOrUnknownIntent_Returns404(t *testing.T) {
	svc := &stubPaymentService{intentErr: payments.ErrIntentNotFound}
	h := newHandler(svc, &stubAccountResolver{accountID: uuid.New()})

	req := httptest.NewRequest(http.MethodGet, intentPath+"?payment_intent_id="+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetCheckoutIntent_MalformedID_Returns400(t *testing.T) {
	svc := &stubPaymentService{}
	h := newHandler(svc, &stubAccountResolver{accountID: uuid.New()})

	req := httptest.NewRequest(http.MethodGet, intentPath+"?payment_intent_id=not-a-uuid", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if svc.intentCalls != 0 {
		t.Errorf("expected no service call for a malformed id, got %d", svc.intentCalls)
	}
}

func TestGetCheckoutIntent_RejectsNonGET(t *testing.T) {
	svc := &stubPaymentService{}
	h := newHandler(svc, &stubAccountResolver{accountID: uuid.New()})

	req := httptest.NewRequest(http.MethodPost, intentPath+"?payment_intent_id="+uuid.New().String(), strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for a write attempt on a read-only return surface, got %d", rr.Code)
	}
	if svc.intentCalls != 0 {
		t.Errorf("expected no service call, got %d", svc.intentCalls)
	}
}
