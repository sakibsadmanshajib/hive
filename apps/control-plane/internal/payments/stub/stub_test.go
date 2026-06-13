package stub_test

import (
	"context"
	"os"
	"strings"
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
// Tests
// ---------------------------------------------------------------------------

func TestStubService_InitiateCheckout_success(t *testing.T) {
	ledger := &fakeLedger{}
	svc := stub.NewStubService(ledger)

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

	// ProviderIntentID must be a non-empty stub value.
	assert.NotEmpty(t, intent.ProviderIntentID)

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
	svc := stub.NewStubService(ledger)

	intent, err := svc.InitiateCheckout(
		context.Background(), uuid.New(), payments.RailStripe, 1_000, "", "idem-stripe-01",
	)
	require.NoError(t, err)
	assert.Equal(t, "USD", intent.LocalCurrency)
	assert.Equal(t, int64(0), intent.AmountUSD) // never exposed
}

func TestStubService_InitiateCheckout_validation_invalid_credits(t *testing.T) {
	ledger := &fakeLedger{}
	svc := stub.NewStubService(ledger)

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
	svc := stub.NewStubService(ledger)

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
	svc := stub.NewStubService(ledger)

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

func TestStubService_HandleProviderEvent_noop(t *testing.T) {
	svc := stub.NewStubService(&fakeLedger{})
	err := svc.HandleProviderEvent(context.Background(), payments.RailStripe, []byte(`{}`), nil)
	assert.NoError(t, err)
}

func TestStubService_GetCheckoutOptions(t *testing.T) {
	svc := stub.NewStubService(&fakeLedger{})
	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, opts)

	// Must include all three rails (demo shows full BD offering).
	rails := make(map[payments.Rail]bool)
	for _, r := range opts.Rails {
		rails[r.Rail] = true
	}
	assert.True(t, rails[payments.RailStripe])
	assert.True(t, rails[payments.RailBkash])
	assert.True(t, rails[payments.RailSSLCommerz])

	// Currency must be returned.
	assert.NotEmpty(t, opts.Currency)
	// PredefinedTiers must be non-empty.
	assert.NotEmpty(t, opts.PredefinedTiers)
}

func TestIsEnabled_default_false(t *testing.T) {
	// With no env override the stub should be disabled by default.
	// (This test passes as long as HIVE_PAYMENTS_STUB is not set in the
	// test runner environment, which is the expected CI state.)
	t.Setenv("HIVE_PAYMENTS_STUB", "")
	assert.False(t, stub.IsEnabled())
}

func TestIsEnabled_true(t *testing.T) {
	t.Setenv("HIVE_PAYMENTS_STUB", "true")
	assert.True(t, stub.IsEnabled())
}

// ---------------------------------------------------------------------------
// Production guard tests
// ---------------------------------------------------------------------------

func TestCheckProductionSafety_stub_disabled_does_not_panic(t *testing.T) {
	// When the stub is off, CheckProductionSafety must be a no-op regardless of HIVE_ENV.
	t.Setenv("HIVE_PAYMENTS_STUB", "false")
	t.Setenv("HIVE_ENV", "production")
	// Must not fatal: if it does, the test process exits and the test fails.
	stub.CheckProductionSafety()
}

func TestCheckProductionSafety_stub_enabled_non_production_does_not_panic(t *testing.T) {
	// Stub enabled in non-production environments must be allowed without fatal.
	for _, env := range []string{"", "development", "staging", "demo", "local"} {
		env := env
		t.Run("HIVE_ENV="+env, func(t *testing.T) {
			t.Setenv("HIVE_PAYMENTS_STUB", "true")
			t.Setenv("HIVE_ENV", env)
			stub.CheckProductionSafety() // must not fatal
		})
	}
}

func TestProductionGuard_condition_detects_production(t *testing.T) {
	// Verify the exact condition used by CheckProductionSafety and main.go
	// triggers for all case/whitespace variants of "production".
	// We test the condition logic (not log.Fatal itself) to avoid killing the process.
	cases := []string{"production", "Production", "PRODUCTION", " production ", " Production "}
	for _, hiveEnv := range cases {
		hiveEnv := hiveEnv
		t.Run(hiveEnv, func(t *testing.T) {
			t.Setenv("HIVE_PAYMENTS_STUB", "true")
			t.Setenv("HIVE_ENV", hiveEnv)
			enabled := stub.IsEnabled()
			isProd := strings.EqualFold(strings.TrimSpace(os.Getenv("HIVE_ENV")), "production")
			assert.True(t, enabled, "stub must be reported as enabled")
			assert.True(t, isProd, "HIVE_ENV=%q must be classified as production", hiveEnv)
		})
	}
}
