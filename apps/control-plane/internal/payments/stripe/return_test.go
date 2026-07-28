package stripe_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	stripego "github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/webhook"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	stripeRail "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/stripe"
)

// stripeAPIStub stands in for the Stripe REST API so Initiate can be exercised
// without a live key. The captured form body is what the assertions read.
type stripeAPIStub struct {
	server *httptest.Server
	paths  []string
	forms  []url.Values
}

func newStripeAPIStub(t *testing.T) *stripeAPIStub {
	t.Helper()
	stub := &stripeAPIStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		stub.paths = append(stub.paths, r.URL.Path)
		stub.forms = append(stub.forms, form)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "cs_test_session_123",
			"object":     "checkout.session",
			"url":        "https://checkout.stripe.com/c/pay/cs_test_session_123",
			"expires_at": time.Now().Add(24 * time.Hour).Unix(),
		})
	}))
	t.Cleanup(stub.server.Close)

	previous := stripego.GetBackend(stripego.APIBackend)
	stripego.SetBackend(stripego.APIBackend, stripego.GetBackendWithConfig(
		stripego.APIBackend,
		&stripego.BackendConfig{URL: stripego.String(stub.server.URL)},
	))
	t.Cleanup(func() { stripego.SetBackend(stripego.APIBackend, previous) })

	return stub
}

func stripeInput() payments.InitiateInput {
	return payments.InitiateInput{
		PaymentIntentID: uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
		AccountID:       uuid.MustParse("223e4567-e89b-12d3-a456-426614174001"),
		Credits:         10000,
		AmountUSD:       100,
		Currency:        "USD",
		CallbackBaseURL: "https://cp.example.com",
		ReturnBaseURL:   "https://console.example.com",
		CustomerName:    "Test User",
		CustomerEmail:   "test@example.com",
	}
}

// TestStripeInitiate_BrowserReturnsGoToTheConsole guards issue #538. The old
// implementation pointed the customer at `${controlPlane}/checkout/stripe`,
// a route that exists in no service, so a paying customer landed on a 404.
func TestStripeInitiate_BrowserReturnsGoToTheConsole(t *testing.T) {
	stub := newStripeAPIStub(t)
	rail := stripeRail.NewRail("sk_test_key", "whsec_test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := stripeInput()
	result, err := rail.Initiate(ctx, input)
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}

	if len(stub.forms) != 1 {
		t.Fatalf("expected 1 Stripe API call, got %d (%v)", len(stub.forms), stub.paths)
	}
	if !strings.Contains(stub.paths[0], "/v1/checkout/sessions") {
		t.Errorf("expected a hosted Checkout Session to be created, got path %q", stub.paths[0])
	}

	form := stub.forms[0]
	for _, field := range []string{"success_url", "cancel_url"} {
		raw := form.Get(field)
		if raw == "" {
			t.Fatalf("%s was not sent", field)
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s does not parse: %v", field, err)
		}
		if parsed.Host != "console.example.com" {
			t.Errorf("%s must target the console origin, got %q", field, raw)
		}
		if parsed.Path != payments.CheckoutReturnPath {
			t.Errorf("%s expected path %q, got %q", field, payments.CheckoutReturnPath, parsed.Path)
		}
		if parsed.Query().Get("intent") != input.PaymentIntentID.String() {
			t.Errorf("%s must carry the intent id, got %q", field, raw)
		}
	}
	if got := form.Get("cancel_url"); !strings.Contains(got, "hint="+payments.ReturnHintCancelled) {
		t.Errorf("cancel_url should carry the cancelled copy hint, got %q", got)
	}
	if got := form.Get("success_url"); strings.Contains(got, "hint=") {
		t.Errorf("success_url must not carry any outcome hint, got %q", got)
	}

	// The customer is sent to the Stripe-hosted page, and the session id is what
	// the webhook will later resolve back to this intent.
	if result.RedirectURL != "https://checkout.stripe.com/c/pay/cs_test_session_123" {
		t.Errorf("expected the hosted Checkout URL, got %q", result.RedirectURL)
	}
	if result.ProviderIntentID != "cs_test_session_123" {
		t.Errorf("expected the session id as provider intent id, got %q", result.ProviderIntentID)
	}
	if strings.Contains(result.RedirectURL, "client_secret") {
		t.Errorf("a client secret must never be placed in a redirect URL: %q", result.RedirectURL)
	}
}

func TestStripeInitiate_RequiresAReturnOrigin(t *testing.T) {
	stub := newStripeAPIStub(t)
	rail := stripeRail.NewRail("sk_test_key", "whsec_test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := stripeInput()
	input.ReturnBaseURL = ""

	if _, err := rail.Initiate(ctx, input); err == nil {
		t.Fatal("expected Initiate to refuse a checkout with no console return origin")
	}
	if len(stub.forms) != 0 {
		t.Error("Stripe must not be called when the return origin is missing")
	}
}

// ---------------------------------------------------------------------------
// Checkout Session webhook events
// ---------------------------------------------------------------------------

func signedSessionEvent(t *testing.T, eventType, sessionID, paymentStatus, secret string) ([]byte, string) {
	t.Helper()

	rawObj, err := json.Marshal(map[string]any{
		"id":             sessionID,
		"object":         "checkout.session",
		"payment_status": paymentStatus,
	})
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}

	rawEvent, err := json.Marshal(map[string]any{
		"object":      "event",
		"type":        eventType,
		"api_version": stripego.APIVersion,
		"data":        map[string]any{"object": json.RawMessage(rawObj)},
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   rawEvent,
		Secret:    secret,
		Timestamp: time.Now(),
	})
	return rawEvent, signed.Header
}

func TestStripeProcessEvent_CheckoutSessionEvents(t *testing.T) {
	const secret = "whsec_testvalidwebhooksecret12345"
	const sessionID = "cs_test_session_123"

	cases := []struct {
		name          string
		eventType     string
		paymentStatus string
		wantEvent     string
		wantErr       bool
	}{
		{"paid session settles", "checkout.session.completed", "paid", "payment.succeeded", false},
		{"unpaid session does not settle", "checkout.session.completed", "unpaid", "", true},
		{"async success settles", "checkout.session.async_payment_succeeded", "paid", "payment.succeeded", false},
		{"async failure fails", "checkout.session.async_payment_failed", "unpaid", "payment.failed", false},
		{"expired session expires", "checkout.session.expired", "unpaid", "payment.expired", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rail := stripeRail.NewRail("sk_test_key", secret)
			rawBody, sig := signedSessionEvent(t, tc.eventType, sessionID, tc.paymentStatus, secret)

			event, err := rail.ProcessEvent(context.Background(), rawBody, map[string]string{
				"Stripe-Signature": sig,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %s/%s", tc.eventType, tc.paymentStatus)
				}
				return
			}
			if err != nil {
				t.Fatalf("ProcessEvent: %v", err)
			}
			if event.EventType != tc.wantEvent {
				t.Errorf("expected %q, got %q", tc.wantEvent, event.EventType)
			}
			if event.ProviderIntentID != sessionID {
				t.Errorf("expected provider intent id %q, got %q", sessionID, event.ProviderIntentID)
			}
		})
	}
}

func TestStripeProcessEvent_RejectsBadSignature(t *testing.T) {
	rail := stripeRail.NewRail("sk_test_key", "whsec_testvalidwebhooksecret12345")
	rawBody, _ := signedSessionEvent(t, "checkout.session.completed", "cs_test_1", "paid", "whsec_someothersecret000000")

	if _, err := rail.ProcessEvent(context.Background(), rawBody, map[string]string{
		"Stripe-Signature": "t=1,v1=deadbeef",
	}); err == nil {
		t.Fatal("expected signature verification to fail")
	}
}
