package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// ---------------------------------------------------------------------------
// Browser return URL construction (issue #538)
// ---------------------------------------------------------------------------
//
// A rail has two distinct outbound URLs and conflating them is the defect this
// suite guards. CallbackBaseURL is the control-plane origin a provider posts
// its server-to-server webhook to. ReturnBaseURL is the console origin the
// customer's browser is sent back to. A browser must never land on a webhook.

func TestBrowserReturnURLTargetsConsoleOriginAndCarriesIntent(t *testing.T) {
	intentID := uuid.New()

	got := BrowserReturnURL("https://console.example.com", CheckoutReturnPath, intentID, RailStripe, "")

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("return URL does not parse: %v", err)
	}
	if parsed.Host != "console.example.com" {
		t.Errorf("expected the console origin, got host %q", parsed.Host)
	}
	if parsed.Path != CheckoutReturnPath {
		t.Errorf("expected path %q, got %q", CheckoutReturnPath, parsed.Path)
	}
	if strings.Contains(parsed.Path, "/webhooks/") {
		t.Errorf("browser return URL must never point at a webhook path: %q", got)
	}
	q := parsed.Query()
	if q.Get("intent") != intentID.String() {
		t.Errorf("expected intent=%s, got %q", intentID, q.Get("intent"))
	}
	if q.Get("rail") != string(RailStripe) {
		t.Errorf("expected rail=stripe, got %q", q.Get("rail"))
	}
	if q.Has("hint") {
		t.Errorf("no hint expected when none was requested, got %q", q.Get("hint"))
	}
}

func TestBrowserReturnURLTrimsTrailingSlashAndAddsCancelledHint(t *testing.T) {
	intentID := uuid.New()

	got := BrowserReturnURL("https://console.example.com/", SSLCommerzReturnPath, intentID, RailSSLCommerz, ReturnHintCancelled)

	if strings.Contains(got, "com//") {
		t.Errorf("trailing slash on the base URL must not double up: %q", got)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("return URL does not parse: %v", err)
	}
	if parsed.Path != SSLCommerzReturnPath {
		t.Errorf("expected path %q, got %q", SSLCommerzReturnPath, parsed.Path)
	}
	if parsed.Query().Get("hint") != ReturnHintCancelled {
		t.Errorf("expected hint=%q, got %q", ReturnHintCancelled, parsed.Query().Get("hint"))
	}
}

func TestValidateReturnBaseURLRejectsUnusableValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		ok    bool
	}{
		{"https origin", "https://console.example.com", true},
		{"http loopback origin", "http://localhost:3000", true},
		{"empty", "", false},
		{"relative", "/console", false},
		{"scheme only", "https://", false},
		{"non http scheme", "javascript:alert(1)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReturnBaseURL(tc.value)
			if tc.ok && err != nil {
				t.Fatalf("expected %q to be accepted, got %v", tc.value, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected %q to be rejected", tc.value)
			}
		})
	}
}

// TestResolveConsoleBaseURLHasNoHardcodedDefault guards against reintroducing a
// plausible looking built-in origin. A hardcoded base URL is what pointed
// payment callbacks at a decommissioned host and made the breakage invisible.
func TestResolveConsoleBaseURLHasNoHardcodedDefault(t *testing.T) {
	t.Setenv("WEB_CONSOLE_PUBLIC_URL", "")
	t.Setenv("NEXT_PUBLIC_APP_URL", "")

	if got := ResolveConsoleBaseURL(); got != "" {
		t.Fatalf("expected no default origin, got %q", got)
	}
	if err := ValidateReturnBaseURL(ResolveConsoleBaseURL()); err == nil {
		t.Fatal("an unconfigured console origin must fail validation")
	}
	if !strings.Contains(ValidateReturnBaseURL("").Error(), "WEB_CONSOLE_PUBLIC_URL") {
		t.Error("the failure should name the variable an operator has to set")
	}
}

func TestResolveConsoleBaseURLPrefersItsOwnVariableThenTheConsoleOrigin(t *testing.T) {
	t.Setenv("WEB_CONSOLE_PUBLIC_URL", "https://console.example.com/")
	t.Setenv("NEXT_PUBLIC_APP_URL", "https://other.example.com")
	if got := ResolveConsoleBaseURL(); got != "https://console.example.com" {
		t.Errorf("expected the dedicated variable to win, got %q", got)
	}

	t.Setenv("WEB_CONSOLE_PUBLIC_URL", "")
	if got := ResolveConsoleBaseURL(); got != "https://other.example.com" {
		t.Errorf("expected the console's own origin as fallback, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Webhook leg: the control-plane origin providers call back on
// ---------------------------------------------------------------------------
//
// The browser return and the webhook are resolved from different variables on
// purpose. These guard the webhook leg, where a loopback default silently made
// every provider callback unreachable.

func TestResolveCallbackBaseURLDemotesALoopbackDefaultBehindARealHost(t *testing.T) {
	t.Setenv("CONTROL_PLANE_PUBLIC_URL", "http://localhost:8081")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", nil)
	req.Host = "control-hive.scubed.co"

	got := resolveCallbackBaseURL(req)
	if got != "https://control-hive.scubed.co" {
		t.Fatalf("a provider cannot reach loopback; expected the real request host, got %q", got)
	}
}

func TestResolveCallbackBaseURLKeepsAConfiguredPublicOrigin(t *testing.T) {
	t.Setenv("CONTROL_PLANE_PUBLIC_URL", "https://cp.example.com/")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", nil)
	req.Host = "internal-lb.local"

	if got := resolveCallbackBaseURL(req); got != "https://cp.example.com" {
		t.Fatalf("expected the configured public origin to win, got %q", got)
	}
}

func TestResolveCallbackBaseURLKeepsLoopbackForALoopbackRequest(t *testing.T) {
	t.Setenv("CONTROL_PLANE_PUBLIC_URL", "http://localhost:8081")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", nil)
	req.Host = "localhost:8081"

	if got := resolveCallbackBaseURL(req); got != "http://localhost:8081" {
		t.Fatalf("local development must stay on loopback, got %q", got)
	}
}

func TestResolveCallbackBaseURLFallsBackToTheRequestHostWhenUnset(t *testing.T) {
	t.Setenv("CONTROL_PLANE_PUBLIC_URL", "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/checkout/initiate", nil)
	req.Host = "control-hive.scubed.co"

	if got := resolveCallbackBaseURL(req); got != "https://control-hive.scubed.co" {
		t.Fatalf("expected the request host, got %q", got)
	}
}

// TestBrowserReturnNeverDependsOnTheControlPlaneOrigin is the hard requirement:
// whatever CONTROL_PLANE_PUBLIC_URL says, it must not influence where a paying
// customer's browser is sent.
func TestBrowserReturnNeverDependsOnTheControlPlaneOrigin(t *testing.T) {
	t.Setenv("CONTROL_PLANE_PUBLIC_URL", "http://localhost:8081")
	t.Setenv("WEB_CONSOLE_PUBLIC_URL", "https://console.example.com")
	t.Setenv("NEXT_PUBLIC_APP_URL", "")

	got := ResolveConsoleBaseURL()
	if got != "https://console.example.com" {
		t.Fatalf("expected the console origin, got %q", got)
	}
	if strings.Contains(BrowserReturnURL(got, CheckoutReturnPath, uuid.New(), RailStripe, ""), "localhost:8081") {
		t.Error("a browser return URL must never carry the control-plane origin")
	}

	// And with no console origin configured at all, the return URL is refused
	// rather than silently borrowing the control-plane value.
	t.Setenv("WEB_CONSOLE_PUBLIC_URL", "")
	if err := ValidateReturnBaseURL(ResolveConsoleBaseURL()); err == nil {
		t.Error("expected checkout to be refused when only the control-plane origin is set")
	}
}

// ---------------------------------------------------------------------------
// Authoritative state mapping
// ---------------------------------------------------------------------------

func TestReturnStateForMapsEveryIntentStatus(t *testing.T) {
	cases := map[IntentStatus]ReturnState{
		IntentStatusCompleted:          ReturnStateSuccess,
		IntentStatusFailed:             ReturnStateFailed,
		IntentStatusExpired:            ReturnStateFailed,
		IntentStatusCancelled:          ReturnStateCancelled,
		IntentStatusCreated:            ReturnStatePending,
		IntentStatusPendingRedirect:    ReturnStatePending,
		IntentStatusProviderProcessing: ReturnStatePending,
		IntentStatusConfirming:         ReturnStatePending,
	}
	for status, want := range cases {
		if got := ReturnStateFor(status); got != want {
			t.Errorf("status %q: expected state %q, got %q", status, want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Service wiring
// ---------------------------------------------------------------------------

// capturingRail records the InitiateInput it was handed so tests can assert on
// the URLs the service passes down to a rail.
type capturingRail struct {
	rail      Rail
	lastInput InitiateInput
}

func (c *capturingRail) RailName() Rail { return c.rail }

func (c *capturingRail) Initiate(_ context.Context, input InitiateInput) (InitiateResult, error) {
	c.lastInput = input
	return InitiateResult{
		ProviderIntentID: "prov_" + string(c.rail),
		RedirectURL:      "https://provider.example.com/pay",
	}, nil
}

func (c *capturingRail) ProcessEvent(_ context.Context, _ []byte, _ map[string]string) (RailEvent, error) {
	return RailEvent{}, errors.New("not used")
}

func returnFlowFixtures() (*stubRepository, *stubLedger, *stubProfiles, *stubFXProvider) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
		billingProfile: profiles.BillingProfile{
			BillingContactName:  "Test Buyer",
			BillingContactEmail: "buyer@example.com",
		},
	}
	fx := &stubFXProvider{snap: FXSnapshot{EffectiveRate: "120.00"}}
	return repo, led, prof, fx
}

func TestInitiateCheckoutPassesBothOriginsToTheRail(t *testing.T) {
	repo, led, prof, fx := returnFlowFixtures()
	rail := &capturingRail{rail: RailStripe}
	svc := NewService(repo, led, prof, fx, map[Rail]PaymentRail{RailStripe: rail})

	if _, err := svc.InitiateCheckout(
		context.Background(), uuid.New(), RailStripe, 1000,
		"https://cp.example.com", "https://console.example.com", "idem-1",
	); err != nil {
		t.Fatalf("InitiateCheckout: %v", err)
	}

	if rail.lastInput.CallbackBaseURL != "https://cp.example.com" {
		t.Errorf("expected the control-plane origin as CallbackBaseURL, got %q", rail.lastInput.CallbackBaseURL)
	}
	if rail.lastInput.ReturnBaseURL != "https://console.example.com" {
		t.Errorf("expected the console origin as ReturnBaseURL, got %q", rail.lastInput.ReturnBaseURL)
	}
}

func TestInitiateCheckoutRefusesAnUnusableReturnBaseURL(t *testing.T) {
	repo, led, prof, fx := returnFlowFixtures()
	rail := &capturingRail{rail: RailStripe}
	svc := NewService(repo, led, prof, fx, map[Rail]PaymentRail{RailStripe: rail})

	_, err := svc.InitiateCheckout(
		context.Background(), uuid.New(), RailStripe, 1000,
		"https://cp.example.com", "", "idem-1",
	)
	if err == nil {
		t.Fatal("expected checkout to be refused when no console return origin is configured")
	}
	if rail.lastInput.PaymentIntentID != uuid.Nil {
		t.Error("the rail must not be called when the return origin is unusable")
	}
}

// ---------------------------------------------------------------------------
// GetCheckoutIntent — read-only, account-scoped
// ---------------------------------------------------------------------------

func TestGetCheckoutIntentReturnsAuthoritativeStateAndGrantsNothing(t *testing.T) {
	repo, led, prof, fx := returnFlowFixtures()
	svc := NewService(repo, led, prof, fx, map[Rail]PaymentRail{})

	accountID := uuid.New()
	intentID := uuid.New()
	if err := repo.InsertPaymentIntent(context.Background(), PaymentIntent{
		ID:        intentID,
		AccountID: accountID,
		Rail:      RailSSLCommerz,
		Status:    IntentStatusConfirming,
		Credits:   5000,
	}); err != nil {
		t.Fatalf("seed intent: %v", err)
	}

	view, err := svc.GetCheckoutIntent(context.Background(), accountID, intentID)
	if err != nil {
		t.Fatalf("GetCheckoutIntent: %v", err)
	}
	if view.State != ReturnStatePending {
		t.Errorf("expected pending while confirming, got %q", view.State)
	}
	if view.Credits != 5000 {
		t.Errorf("expected 5000 credits, got %d", view.Credits)
	}

	// Replaying the read must never move money. This is the crafted-return-URL
	// guard: a return surface reads state, settlement is the webhook's job.
	for i := 0; i < 5; i++ {
		if _, err := svc.GetCheckoutIntent(context.Background(), accountID, intentID); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	if led.callCount() != 0 {
		t.Errorf("expected zero ledger grants from reading a return state, got %d", led.callCount())
	}
	stored, err := repo.GetPaymentIntent(context.Background(), intentID)
	if err != nil {
		t.Fatalf("reload intent: %v", err)
	}
	if stored.Status != IntentStatusConfirming {
		t.Errorf("reading the return state must not advance the intent, got %q", stored.Status)
	}
}

func TestGetCheckoutIntentResolvesPendingToSuccessOnceSettled(t *testing.T) {
	repo, led, prof, fx := returnFlowFixtures()
	svc := NewService(repo, led, prof, fx, map[Rail]PaymentRail{})

	accountID := uuid.New()
	intentID := uuid.New()
	if err := repo.InsertPaymentIntent(context.Background(), PaymentIntent{
		ID:        intentID,
		AccountID: accountID,
		Rail:      RailSSLCommerz,
		Status:    IntentStatusConfirming,
		Credits:   1000,
	}); err != nil {
		t.Fatalf("seed intent: %v", err)
	}

	before, err := svc.GetCheckoutIntent(context.Background(), accountID, intentID)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if before.State != ReturnStatePending {
		t.Fatalf("expected pending before settlement, got %q", before.State)
	}

	// Settlement lands through the normal server-side path.
	if _, err := repo.CompareAndSetStatus(context.Background(), intentID, IntentStatusConfirming, IntentStatusCompleted); err != nil {
		t.Fatalf("settle intent: %v", err)
	}

	after, err := svc.GetCheckoutIntent(context.Background(), accountID, intentID)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if after.State != ReturnStateSuccess {
		t.Errorf("expected success after settlement, got %q", after.State)
	}
}

func TestGetCheckoutIntentHidesIntentsOwnedByAnotherAccount(t *testing.T) {
	repo, led, prof, fx := returnFlowFixtures()
	svc := NewService(repo, led, prof, fx, map[Rail]PaymentRail{})

	ownerID := uuid.New()
	intentID := uuid.New()
	if err := repo.InsertPaymentIntent(context.Background(), PaymentIntent{
		ID:        intentID,
		AccountID: ownerID,
		Rail:      RailStripe,
		Status:    IntentStatusCompleted,
		Credits:   100000,
	}); err != nil {
		t.Fatalf("seed intent: %v", err)
	}

	_, err := svc.GetCheckoutIntent(context.Background(), uuid.New(), intentID)
	if !errors.Is(err, ErrIntentNotFound) {
		t.Fatalf("expected ErrIntentNotFound for a foreign intent, got %v", err)
	}
}
