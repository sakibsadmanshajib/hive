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
		"XE_ACCOUNT_ID":           "xe-account-placeholder",
		"XE_API_KEY":              "xe-key-placeholder",
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
	blank := map[string]string{
		"SSLCOMMERZ_STORE_ID":     "  ",
		"SSLCOMMERZ_STORE_PASSWD": "passwd",
		"XE_ACCOUNT_ID":           "xe-account-placeholder",
		"XE_API_KEY":              "xe-key-placeholder",
	}
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

// TestMissingRailCredentials_BDRailsNeedTheirFXCredentials covers the rail that
// registers and then cannot price anything. A BD checkout takes an FX snapshot
// before it reaches the rail, and the FX service is built from XE_ACCOUNT_ID and
// XE_API_KEY, so a bKash rail with four bKash credentials and no XE advertises
// itself as enabled and dies inside CreateSnapshot on every attempt, with no
// warning naming the variable that is actually unset.
func TestMissingRailCredentials_BDRailsNeedTheirFXCredentials(t *testing.T) {
	bdWithoutFX := map[string]string{
		"BKASH_APP_KEY":           "app-key",
		"BKASH_APP_SECRET":        "app-secret",
		"BKASH_USERNAME":          "user",
		"BKASH_PASSWORD":          "pass",
		"SSLCOMMERZ_STORE_ID":     "store",
		"SSLCOMMERZ_STORE_PASSWD": "passwd",
	}
	lookup := func(key string) string { return bdWithoutFX[key] }

	for _, rail := range []Rail{RailBkash, RailSSLCommerz} {
		missing := MissingRailCredentials(rail, lookup)
		if len(missing) != 2 || missing[0] != "XE_ACCOUNT_ID" || missing[1] != "XE_API_KEY" {
			t.Errorf("%s: a BD rail with no FX credentials must report exactly the XE pair, got %v", rail, missing)
		}
	}

	// The non-BD rail prices in USD cents and never touches FX, so it must not
	// acquire a dependency it does not have.
	stripeOnly := map[string]string{
		"STRIPE_SECRET_KEY":     "sk_test_placeholder",
		"STRIPE_WEBHOOK_SECRET": "whsec_placeholder",
	}
	if missing := MissingRailCredentials(RailStripe, func(key string) string { return stripeOnly[key] }); len(missing) != 0 {
		t.Errorf("stripe must not require FX credentials, got %v", missing)
	}
}

// TestMissingRailCredentials_PresentButEmptyIsUnconfigured pins the deployed
// box's own shape. Compose injects all twelve payment variables through
// ${VAR:-} defaults, so on that box every name is present and every value is
// empty. A presence check would read three fully configured rails there. This
// case is what goes red if the port is ever relaxed from a plain string lookup
// to os.LookupEnv presence.
func TestMissingRailCredentials_PresentButEmptyIsUnconfigured(t *testing.T) {
	presentButEmpty := map[string]string{
		"STRIPE_SECRET_KEY": "", "STRIPE_WEBHOOK_SECRET": "",
		"BKASH_APP_KEY": "", "BKASH_APP_SECRET": "", "BKASH_USERNAME": "", "BKASH_PASSWORD": "",
		"SSLCOMMERZ_STORE_ID": "", "SSLCOMMERZ_STORE_PASSWD": "",
		"BKASH_BASE_URL": "", "SSLCOMMERZ_BASE_URL": "",
		"XE_ACCOUNT_ID": "", "XE_API_KEY": "",
	}
	lookup := func(key string) string {
		value, present := presentButEmpty[key]
		if !present {
			t.Fatalf("fixture is meant to hold every payment variable, %s was absent", key)
		}
		return value
	}

	for _, rail := range []Rail{RailStripe, RailBkash, RailSSLCommerz} {
		got := MissingRailCredentials(rail, lookup)
		if len(got) != len(RailCredentialEnvs[rail]) {
			t.Errorf("%s: a present-but-empty set must be wholly missing, got %v", rail, got)
		}
	}

	// And the registration decision has to agree, which is the property that
	// actually keeps the box's rails unregistered.
	rails, refusals := RegisterRails(lookup, allRailBuilders(t))
	if len(rails) != 0 {
		t.Errorf("no rail may be registered from present-but-empty credentials, got %v", rails)
	}
	if len(refusals) != 3 {
		t.Errorf("every rail must be refused by name, got %v", refusals)
	}
}

// allRailBuilders mirrors main()'s builder list with stub constructors. Build
// records that it ran, so a test can tell "not registered" from "registered and
// then discarded".
func allRailBuilders(t *testing.T) []RailBuilder {
	t.Helper()
	build := func(rail Rail) func() (PaymentRail, error) {
		return func() (PaymentRail, error) { return newStubRail(rail), nil }
	}
	return []RailBuilder{
		{Rail: RailStripe, Build: build(RailStripe)},
		{Rail: RailBkash, Build: build(RailBkash)},
		{Rail: RailSSLCommerz, Build: build(RailSSLCommerz)},
	}
}

// TestRegisterRails_HalfACredentialSetRegistersNothing is the test the review
// found missing: it observes the registration decision itself, not the predicate
// underneath it. Relax the gate to a presence check on one variable, which is
// what the reported defect was, and this goes red.
func TestRegisterRails_HalfACredentialSetRegistersNothing(t *testing.T) {
	built := map[Rail]int{}
	builders := []RailBuilder{
		{Rail: RailStripe, Build: func() (PaymentRail, error) {
			built[RailStripe]++
			return newStubRail(RailStripe), nil
		}},
		{Rail: RailBkash, Build: func() (PaymentRail, error) {
			built[RailBkash]++
			return newStubRail(RailBkash), nil
		}},
	}

	// A Stripe secret key with no webhook secret is the reported defect: the rail
	// would redirect a payer and take their money, then fail signature
	// verification on every settlement event.
	halfStripe := map[string]string{"STRIPE_SECRET_KEY": "sk_test_placeholder"}
	rails, refusals := RegisterRails(func(key string) string { return halfStripe[key] }, builders)

	if _, registered := rails[RailStripe]; registered {
		t.Error("a Stripe rail that cannot verify a webhook must not be registered")
	}
	if built[RailStripe] != 0 {
		t.Errorf("the constructor must not even run on an incomplete set, ran %d time(s)", built[RailStripe])
	}
	if len(rails) != 0 {
		t.Errorf("no rail is fully credentialed here, got %v", rails)
	}
	if len(refusals) != 2 {
		t.Fatalf("expected both rails refused, got %v", refusals)
	}
	if refusals[0].Rail != RailStripe || len(refusals[0].Missing) != 1 || refusals[0].Missing[0] != "STRIPE_WEBHOOK_SECRET" {
		t.Errorf("the refusal must name the one unset variable an operator has to fix, got %+v", refusals[0])
	}
	if refusals[1].Rail != RailBkash {
		t.Errorf("refusals must stay in builder order so the boot log is stable, got %+v", refusals[1])
	}
}

// TestRegisterRails_WholeSetRegisters is the other half: the gate must not be so
// tight that a correctly configured box registers nothing, which would be a
// green test suite over a dead checkout.
func TestRegisterRails_WholeSetRegisters(t *testing.T) {
	full := map[string]string{
		"STRIPE_SECRET_KEY":       "sk_test_placeholder",
		"STRIPE_WEBHOOK_SECRET":   "whsec_placeholder",
		"BKASH_APP_KEY":           "app-key",
		"BKASH_APP_SECRET":        "app-secret",
		"BKASH_USERNAME":          "user",
		"BKASH_PASSWORD":          "pass",
		"SSLCOMMERZ_STORE_ID":     "store",
		"SSLCOMMERZ_STORE_PASSWD": "passwd",
		"XE_ACCOUNT_ID":           "xe-account-placeholder",
		"XE_API_KEY":              "xe-key-placeholder",
	}
	rails, refusals := RegisterRails(func(key string) string { return full[key] }, allRailBuilders(t))
	if len(rails) != 3 || len(refusals) != 0 {
		t.Fatalf("a complete configuration must register every rail, got %d rail(s) and refusals %v", len(rails), refusals)
	}
}

// TestRegisterRails_ConstructorRefusalIsNotARegistration keeps the constructor
// guard and the registration gate as two independent layers. If a rail's own
// constructor refuses despite a complete-looking set, the rail stays out of the
// map rather than being registered as a nil interface value that would panic on
// the first checkout.
func TestRegisterRails_ConstructorRefusalIsNotARegistration(t *testing.T) {
	full := map[string]string{"STRIPE_SECRET_KEY": "sk_test_placeholder", "STRIPE_WEBHOOK_SECRET": "whsec_placeholder"}
	refusal := errors.New("stripe: refusing to build a rail")
	rails, refusals := RegisterRails(
		func(key string) string { return full[key] },
		[]RailBuilder{{Rail: RailStripe, Build: func() (PaymentRail, error) { return nil, refusal }}},
	)
	if len(rails) != 0 {
		t.Errorf("a constructor refusal must leave the rail unregistered, got %v", rails)
	}
	if len(refusals) != 1 || !errors.Is(refusals[0].Err, refusal) {
		t.Fatalf("the constructor's reason must reach the boot log, got %+v", refusals)
	}
}

// TestInitiateCheckout_RailNotAvailableForCountryIsACustomerFault guards the
// order of the two refusals. A non-BD account naming a BD rail is bad customer
// input and answers 400; only a rail the caller could legitimately select can
// reach the deployment-fault refusal. Asking the configuration question first
// turned customer input into a 5xx and told an authenticated caller which rails
// the box holds credentials for outside its own country set.
func TestInitiateCheckout_RailNotAvailableForCountryIsACustomerFault(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	svc := buildService(
		repo,
		led,
		completeCheckoutProfiles("US"),
		&stubFXProvider{snap: FXSnapshot{EffectiveRate: "115.500000"}},
		map[Rail]PaymentRail{},
	)

	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailBkash, MinPurchaseCredits,
		"https://control.example.test", "https://console.example.test", "idem-1449-country-first")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if errors.Is(err, ErrRailNotConfigured) {
		t.Errorf("a rail this account was never entitled to select must not be answered as a deployment fault: %v", err)
	}
	if !strings.Contains(err.Error(), "not available for country") {
		t.Errorf("expected the country refusal, got %v", err)
	}
	if n := intentCount(repo); n != 0 {
		t.Errorf("a refused checkout must create nothing, got %d intent(s)", n)
	}
}

// TestInitiateCheckout_FailedInitiateLeavesNoCreatedIntent closes the other half
// of the stranded-row class the review found. The insert stays ahead of the
// provider call on purpose (it is the durable record that a provider-side
// session belongs to an account, and the unique index on account and idempotency
// key is the only guard against a double submit opening two of them), so the
// attempts that fail after it have to reach a terminal state themselves rather
// than waiting for a reaper that does not exist.
func TestInitiateCheckout_FailedInitiateLeavesNoCreatedIntent(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	failing := newStubRail(RailStripe)
	failing.initErr = errors.New("provider refused the session")

	svc := buildService(
		repo,
		led,
		completeCheckoutProfiles("US"),
		&stubFXProvider{snap: FXSnapshot{EffectiveRate: "115.500000"}},
		map[Rail]PaymentRail{RailStripe: failing},
	)

	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailStripe, MinPurchaseCredits,
		"https://control.example.test", "https://console.example.test", "idem-1449-initiate-fail")
	if err == nil {
		t.Fatal("expected the rail failure to surface")
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.intents) != 1 {
		t.Fatalf("expected the one attempted intent, got %d", len(repo.intents))
	}
	for _, intent := range repo.intents {
		if intent.Status == IntentStatusCreated {
			t.Errorf("a failed initiate must not strand a created intent, got status %q", intent.Status)
		}
		if intent.Status != IntentStatusFailed {
			t.Errorf("expected a terminal failed status, got %q", intent.Status)
		}
	}
	if led.callCount() != 0 {
		t.Errorf("no credit may be granted for a payment that never started, got %d grant(s)", led.callCount())
	}
}
