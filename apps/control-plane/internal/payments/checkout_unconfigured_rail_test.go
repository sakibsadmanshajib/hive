package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Issue #1449. A deployment that holds no credentials for a rail cannot take a
// payment on it and cannot settle one either, so a checkout aimed at that rail
// has exactly one honest outcome: refuse, before anything exists and before any
// credit moves.
//
// The old order reached the refusal last. It resolved the country, loaded the
// billing profile, took an FX snapshot on a BD rail, inserted the payment intent
// row, and only then looked the rail implementation up and failed. That left a
// stranded `created` intent per attempt, and answered the customer with a 400
// "checkout failed" that reads as the customer's fault.

// completeCheckoutProfiles returns a profile pair complete enough that nothing
// upstream of the rail lookup can be what refuses the checkout.
func completeCheckoutProfiles(country string) *stubProfiles {
	return &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: country},
		billingProfile: profiles.BillingProfile{
			BillingContactName:  "Demo Buyer",
			BillingContactEmail: "buyer@example.test",
			CountryCode:         country,
		},
	}
}

func intentCount(r *stubRepository) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.intents)
}

func fxSnapshotCount(r *stubRepository) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.snaps)
}

func TestInitiateCheckout_UnconfiguredRailGrantsNoCreditAndCreatesNothing(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	svc := buildService(
		repo,
		led,
		completeCheckoutProfiles(""),
		&stubFXProvider{snap: FXSnapshot{EffectiveRate: "115.500000"}},
		// No rail is registered, which is exactly what a box with no payment
		// credentials builds.
		map[Rail]PaymentRail{},
	)

	_, err := svc.InitiateCheckout(
		context.Background(),
		uuid.New(),
		RailStripe,
		MinPurchaseCredits,
		"https://control.example.test",
		"https://console.example.test",
		"idem-1449-stripe",
	)

	if !errors.Is(err, ErrRailNotConfigured) {
		t.Fatalf("expected ErrRailNotConfigured, got %v", err)
	}
	// The bound that matters on a money path: nothing was granted.
	if led.callCount() != 0 {
		t.Fatalf("no credit may be granted without a settled payment, got %d ledger grant(s)", led.callCount())
	}
	if n := intentCount(repo); n != 0 {
		t.Fatalf("refusal must precede the intent insert, got %d stranded intent(s)", n)
	}
	// The server-side error names the variables an operator has to set, the
	// same way ErrReturnURLNotConfigured does.
	for _, want := range RailCredentialEnvs[RailStripe] {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("server-side error must name the missing credential %s, got %q", want, err.Error())
		}
	}
}

func TestInitiateCheckout_UnconfiguredBDRailTakesNoFXSnapshot(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	svc := buildService(
		repo,
		led,
		completeCheckoutProfiles("BD"),
		&stubFXProvider{snap: FXSnapshot{EffectiveRate: "115.500000"}},
		map[Rail]PaymentRail{},
	)

	_, err := svc.InitiateCheckout(
		context.Background(),
		uuid.New(),
		RailBkash,
		MinPurchaseCredits,
		"https://control.example.test",
		"https://console.example.test",
		"idem-1449-bkash",
	)

	if !errors.Is(err, ErrRailNotConfigured) {
		t.Fatalf("expected ErrRailNotConfigured, got %v", err)
	}
	if led.callCount() != 0 {
		t.Fatalf("no credit may be granted without a settled payment, got %d ledger grant(s)", led.callCount())
	}
	if n := fxSnapshotCount(repo); n != 0 {
		t.Fatalf("an unbuyable rail must not burn an FX snapshot, got %d", n)
	}
	if n := intentCount(repo); n != 0 {
		t.Fatalf("refusal must precede the intent insert, got %d stranded intent(s)", n)
	}
}

// initiateResolver answers with a fixed account so the endpoint test exercises
// the checkout, not the auth boundary.
type initiateResolver struct{ accountID uuid.UUID }

func (r *initiateResolver) EnsureViewerContext(_ context.Context) (uuid.UUID, error) {
	return r.accountID, nil
}

func TestInitiateEndpoint_UnconfiguredRailRefusesProviderBlind(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	svc := buildService(
		repo,
		led,
		completeCheckoutProfiles(""),
		&stubFXProvider{snap: FXSnapshot{EffectiveRate: "115.500000"}},
		map[Rail]PaymentRail{},
	)
	h := NewHandler(svc, &initiateResolver{accountID: uuid.New()})

	body, err := json.Marshal(map[string]any{
		"rail":            string(RailStripe),
		"credits":         MinPurchaseCredits,
		"idempotency_key": "idem-1449-endpoint",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", strings.NewReader(string(body)))
	req.Host = "control.example.test"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// A deployment fault, not a customer fault, so it is a 5xx and never a 400
	// that blames the payer for a box with no credentials.
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for a rail this deployment cannot use, got %d body=%s", rr.Code, rr.Body.String())
	}

	wire := strings.ToLower(rr.Body.String())
	// Provider-blind: no rail brand, and no operator-facing variable names,
	// reach the customer.
	for _, leak := range []string{"stripe", "bkash", "sslcommerz", "secret_key", "webhook", "credential", "env"} {
		if strings.Contains(wire, leak) {
			t.Errorf("customer wire must not carry %q, got %s", leak, rr.Body.String())
		}
	}
	// It still has to say something the payer can act on.
	if !strings.Contains(wire, "unavailable") {
		t.Errorf("refusal must tell the customer the payment method is unavailable, got %s", rr.Body.String())
	}
	if led.callCount() != 0 {
		t.Fatalf("no credit may be granted without a settled payment, got %d ledger grant(s)", led.callCount())
	}
	if n := intentCount(repo); n != 0 {
		t.Fatalf("refusal must precede the intent insert, got %d stranded intent(s)", n)
	}
}

func TestMissingRailCredentials(t *testing.T) {
	full := map[string]string{
		"STRIPE_SECRET_KEY":       "sk_test_placeholder",
		"STRIPE_WEBHOOK_SECRET":   "whsec_placeholder",
		"BKASH_APP_KEY":           "app-key",
		"BKASH_APP_SECRET":        "app-secret",
		"BKASH_USERNAME":          "user",
		"BKASH_PASSWORD":          "pass",
		"SSLCOMMERZ_STORE_ID":     "store",
		"SSLCOMMERZ_STORE_PASSWD": "passwd",
	}
	lookupFrom := func(env map[string]string) func(string) string {
		return func(key string) string { return env[key] }
	}

	for _, rail := range []Rail{RailStripe, RailBkash, RailSSLCommerz} {
		if missing := MissingRailCredentials(rail, lookupFrom(full)); len(missing) != 0 {
			t.Errorf("%s: a complete credential set must report nothing missing, got %v", rail, missing)
		}
	}

	// The half-configured case is the dangerous one: a Stripe rail with no
	// webhook secret can redirect a payer and take their money, and then fails
	// every signature check, so the payment settles into nothing.
	partial := map[string]string{"STRIPE_SECRET_KEY": "sk_test_placeholder"}
	missing := MissingRailCredentials(RailStripe, lookupFrom(partial))
	if len(missing) != 1 || missing[0] != "STRIPE_WEBHOOK_SECRET" {
		t.Errorf("expected exactly STRIPE_WEBHOOK_SECRET missing, got %v", missing)
	}

	// Whitespace is not a credential.
	blank := map[string]string{"SSLCOMMERZ_STORE_ID": "  ", "SSLCOMMERZ_STORE_PASSWD": "passwd"}
	if missing := MissingRailCredentials(RailSSLCommerz, lookupFrom(blank)); len(missing) != 1 || missing[0] != "SSLCOMMERZ_STORE_ID" {
		t.Errorf("expected a whitespace-only value to count as missing, got %v", missing)
	}

	// Nothing set at all reports the whole set, in declaration order, which is
	// what the boot log prints.
	none := func(string) string { return "" }
	if got := MissingRailCredentials(RailBkash, none); len(got) != len(RailCredentialEnvs[RailBkash]) {
		t.Errorf("expected the full bkash credential set, got %v", got)
	}
}
