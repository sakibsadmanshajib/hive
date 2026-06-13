package stub_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/stub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fake ledger for tests — counts calls and records idempotency keys.
// ---------------------------------------------------------------------------

type fakeLedger struct {
	calls []struct {
		accountID      uuid.UUID
		idempotencyKey string
		credits        int64
	}
	failErr error
}

func (f *fakeLedger) GrantCredits(
	_ context.Context,
	accountID uuid.UUID,
	idempotencyKey string,
	credits int64,
	_ map[string]any,
) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.calls = append(f.calls, struct {
		accountID      uuid.UUID
		idempotencyKey string
		credits        int64
	}{accountID, idempotencyKey, credits})
	return nil
}

// ---------------------------------------------------------------------------
// Fake country reader — returns a fixed country code (and optional error).
// ---------------------------------------------------------------------------

type fakeCountry struct {
	code string
	err  error
}

func (f *fakeCountry) CountryCode(_ context.Context, _ uuid.UUID) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.code, nil
}

// newStub builds a StubService for a given country code (defaults to BD so all
// rails are available unless a test overrides it).
func newStub(ledger stub.LedgerGranter, country string) *stub.StubService {
	return stub.NewStubService(ledger, &fakeCountry{code: country})
}

// ---------------------------------------------------------------------------
// InitiateCheckout
// ---------------------------------------------------------------------------

func TestStubService_InitiateCheckout_success(t *testing.T) {
	ledger := &fakeLedger{}
	svc := newStub(ledger, "BD")

	accountID := uuid.New()
	credits := int64(10_000)
	idempKey := "test-idem-key-001"

	intent, err := svc.InitiateCheckout(context.Background(), accountID, payments.RailBkash, credits, "", idempKey)
	require.NoError(t, err)
	require.NotNil(t, intent)

	// Status must be completed immediately.
	assert.Equal(t, payments.IntentStatusCompleted, intent.Status)
	assert.Equal(t, credits, intent.Credits)
	assert.Equal(t, accountID, intent.AccountID)
	assert.Equal(t, payments.RailBkash, intent.Rail)
	assert.Equal(t, idempKey, intent.IdempotencyKey)

	// BD regulatory: AmountUSD must be zero (not exposed to customer).
	assert.Equal(t, int64(0), intent.AmountUSD)

	// Local currency for bKash must be BDT.
	assert.Equal(t, "BDT", intent.LocalCurrency)

	// Provider-blind: ProviderIntentID must be a non-empty plain UUID with no
	// "stub" marker so stub mode is undetectable from the response.
	assert.NotEmpty(t, intent.ProviderIntentID)
	assert.NotContains(t, intent.ProviderIntentID, "stub")
	_, parseErr := uuid.Parse(intent.ProviderIntentID)
	assert.NoError(t, parseErr, "ProviderIntentID must be a plain UUID")

	// Ledger must have been called exactly once.
	require.Len(t, ledger.calls, 1)
	call := ledger.calls[0]
	assert.Equal(t, accountID, call.accountID)
	assert.Equal(t, credits, call.credits)
	// Ledger key must be deterministic and scoped to prevent collisions.
	assert.Equal(t, "stub:purchase:"+idempKey, call.idempotencyKey)
}

func TestStubService_InitiateCheckout_stripeRail_usdCurrency(t *testing.T) {
	ledger := &fakeLedger{}
	svc := newStub(ledger, "BD")

	intent, err := svc.InitiateCheckout(
		context.Background(), uuid.New(), payments.RailStripe, 1_000, "", "idem-stripe-01",
	)
	require.NoError(t, err)
	assert.Equal(t, "USD", intent.LocalCurrency)
	assert.Equal(t, int64(0), intent.AmountUSD) // never exposed
}

func TestStubService_InitiateCheckout_validation_invalid_credits(t *testing.T) {
	ledger := &fakeLedger{}
	svc := newStub(ledger, "BD")

	// Zero credits.
	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), payments.RailStripe, 0, "", "idem-1")
	assert.Error(t, err)
	assert.Len(t, ledger.calls, 0, "ledger must not be called on invalid input")

	// Negative credits.
	_, err = svc.InitiateCheckout(context.Background(), uuid.New(), payments.RailStripe, -1000, "", "idem-2")
	assert.Error(t, err)

	// Not a multiple of 1000.
	_, err = svc.InitiateCheckout(context.Background(), uuid.New(), payments.RailStripe, 1500, "", "idem-3")
	assert.Error(t, err)
}

func TestStubService_InitiateCheckout_validation_empty_idempotency_key(t *testing.T) {
	ledger := &fakeLedger{}
	svc := newStub(ledger, "BD")

	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), payments.RailStripe, 1_000, "", "")
	assert.Error(t, err)
	assert.Len(t, ledger.calls, 0)
}

func TestStubService_InitiateCheckout_idempotency_deduplication(t *testing.T) {
	// Same idempotency key called twice must produce the same ledger key.
	// The fakeLedger records both calls; idempotency deduplication is
	// enforced by the real ledger (ON CONFLICT). Here we verify the stub
	// sends the same deterministic ledger key both times.
	ledger := &fakeLedger{}
	svc := newStub(ledger, "BD")

	accountID := uuid.New()
	idem := "stable-idem-key"

	_, err := svc.InitiateCheckout(context.Background(), accountID, payments.RailStripe, 1_000, "", idem)
	require.NoError(t, err)
	_, err = svc.InitiateCheckout(context.Background(), accountID, payments.RailStripe, 1_000, "", idem)
	require.NoError(t, err)

	require.Len(t, ledger.calls, 2)
	// Both must use the same deterministic ledger key.
	assert.Equal(t, ledger.calls[0].idempotencyKey, ledger.calls[1].idempotencyKey)
}

// ---------------------------------------------------------------------------
// Country to rail access control (must mirror payments.AvailableRails).
// ---------------------------------------------------------------------------

func TestStubService_InitiateCheckout_nonBD_rejects_bkash_and_sslcommerz(t *testing.T) {
	// A non-BD account must not be able to select bKash or SSLCommerz.
	for _, rail := range []payments.Rail{payments.RailBkash, payments.RailSSLCommerz} {
		rail := rail
		t.Run(string(rail), func(t *testing.T) {
			ledger := &fakeLedger{}
			svc := newStub(ledger, "US")

			_, err := svc.InitiateCheckout(context.Background(), uuid.New(), rail, 1_000, "", "idem-nonbd")
			require.Error(t, err)
			assert.Len(t, ledger.calls, 0, "ledger must not be credited for a disallowed rail")
		})
	}
}

func TestStubService_InitiateCheckout_nonBD_allows_stripe(t *testing.T) {
	ledger := &fakeLedger{}
	svc := newStub(ledger, "US")

	intent, err := svc.InitiateCheckout(context.Background(), uuid.New(), payments.RailStripe, 1_000, "", "idem-us-stripe")
	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.Len(t, ledger.calls, 1)
}

func TestStubService_InitiateCheckout_BD_allows_all_rails(t *testing.T) {
	for _, rail := range payments.AvailableRails("BD") {
		rail := rail
		t.Run(string(rail), func(t *testing.T) {
			ledger := &fakeLedger{}
			svc := newStub(ledger, "BD")

			_, err := svc.InitiateCheckout(context.Background(), uuid.New(), rail, 1_000, "", "idem-bd")
			require.NoError(t, err)
			assert.Len(t, ledger.calls, 1)
		})
	}
}

// ---------------------------------------------------------------------------
// HandleProviderEvent must reject (not silently swallow) webhooks.
// ---------------------------------------------------------------------------

func TestStubService_HandleProviderEvent_rejects(t *testing.T) {
	svc := newStub(&fakeLedger{}, "BD")
	err := svc.HandleProviderEvent(context.Background(), payments.RailStripe, []byte(`{}`), nil)
	require.Error(t, err, "stub must reject provider webhooks, not silently accept them")
}

// ---------------------------------------------------------------------------
// GetCheckoutOptions must be filtered by country.
// ---------------------------------------------------------------------------

func TestStubService_GetCheckoutOptions_BD_includesAllRails(t *testing.T) {
	svc := newStub(&fakeLedger{}, "BD")
	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, opts)

	rails := make(map[payments.Rail]bool)
	for _, r := range opts.Rails {
		rails[r.Rail] = true
	}
	assert.True(t, rails[payments.RailStripe])
	assert.True(t, rails[payments.RailBkash])
	assert.True(t, rails[payments.RailSSLCommerz])

	assert.Equal(t, "BDT", opts.Currency)
	assert.NotEmpty(t, opts.PredefinedTiers)
}

func TestStubService_GetCheckoutOptions_nonBD_stripeOnly(t *testing.T) {
	svc := newStub(&fakeLedger{}, "US")
	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, opts)

	rails := make(map[payments.Rail]bool)
	for _, r := range opts.Rails {
		rails[r.Rail] = true
	}
	assert.True(t, rails[payments.RailStripe], "non-BD must offer Stripe")
	assert.False(t, rails[payments.RailBkash], "non-BD must NOT offer bKash")
	assert.False(t, rails[payments.RailSSLCommerz], "non-BD must NOT offer SSLCommerz")

	assert.Equal(t, "USD", opts.Currency)
}

// ---------------------------------------------------------------------------
// IsEnabled
// ---------------------------------------------------------------------------

func TestIsEnabled_default_false(t *testing.T) {
	// With no env override the stub should be disabled by default.
	t.Setenv("HIVE_PAYMENTS_STUB", "")
	assert.False(t, stub.IsEnabled())
}

func TestIsEnabled_true(t *testing.T) {
	t.Setenv("HIVE_PAYMENTS_STUB", "true")
	assert.True(t, stub.IsEnabled())
}

// ---------------------------------------------------------------------------
// Production guard — exercise the REAL exported guard functions
// (EnvIsSafe / IsEnabled / CheckProductionSafety), table-driven over HIVE_ENV,
// so a refactor that breaks the allowlist is caught here. We deliberately do
// NOT reconstruct the condition inline.
//
// We cannot assert the log.Fatal path directly (it would exit the test
// process), so the fatal-trigger combinations are asserted via EnvIsSafe()
// returning false; the no-op combinations call CheckProductionSafety() which
// must not exit.
// ---------------------------------------------------------------------------

func TestEnvIsSafe_allowlist(t *testing.T) {
	cases := []struct {
		hiveEnv string
		safe    bool
	}{
		// Allowed (allowlist members, including case/whitespace variants).
		{"demo", true},
		{"staging", true},
		{"local", true},
		{"development", true},
		{"test", true},
		{"DEMO", true},
		{" Staging ", true},
		// Disallowed — fail closed.
		{"", false},
		{"production", false},
		{"Production", false},
		{" PRODUCTION ", false},
		{"prod", false},
		{"unknown", false},
		{"qa", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("HIVE_ENV="+tc.hiveEnv, func(t *testing.T) {
			t.Setenv("HIVE_ENV", tc.hiveEnv)
			assert.Equal(t, tc.safe, stub.EnvIsSafe(),
				"HIVE_ENV=%q safety classification", tc.hiveEnv)
		})
	}
}

func TestCheckProductionSafety_stub_disabled_is_noop(t *testing.T) {
	// When the stub is off, CheckProductionSafety must be a no-op regardless of
	// HIVE_ENV. If it fataled, the test process would exit and the test fail.
	t.Setenv("HIVE_PAYMENTS_STUB", "false")
	t.Setenv("HIVE_ENV", "production")
	stub.CheckProductionSafety()
}

func TestCheckProductionSafety_stub_enabled_safe_env_is_noop(t *testing.T) {
	// Stub enabled in an allowlisted environment must not fatal.
	for _, env := range []string{"demo", "staging", "local", "development", "test"} {
		env := env
		t.Run("HIVE_ENV="+env, func(t *testing.T) {
			t.Setenv("HIVE_PAYMENTS_STUB", "true")
			t.Setenv("HIVE_ENV", env)
			stub.CheckProductionSafety() // must not fatal
		})
	}
}

func TestCheckProductionSafety_stub_enabled_unsafe_env_would_fatal(t *testing.T) {
	// For unsafe environments (including unset), the guard MUST trip. We assert
	// the precondition (stub enabled AND env not safe) via the real exported
	// functions rather than calling CheckProductionSafety (which would exit).
	for _, env := range []string{"", "production", "prod", "qa", "unknown"} {
		env := env
		t.Run("HIVE_ENV="+env, func(t *testing.T) {
			t.Setenv("HIVE_PAYMENTS_STUB", "true")
			t.Setenv("HIVE_ENV", env)
			require.True(t, stub.IsEnabled(), "stub must be enabled")
			assert.False(t, stub.EnvIsSafe(),
				"HIVE_ENV=%q must be classified unsafe so CheckProductionSafety fatals", env)
		})
	}
}
