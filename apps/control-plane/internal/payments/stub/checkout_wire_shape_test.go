package stub_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The demo box runs the stub (HIVE_PAYMENTS_STUB=true), so this is the payload
// the console actually renders there. It has to satisfy the same decoder as the
// production one, or "Buy credits" is dead on the demo whatever the real
// service does (issue #1386).
func TestStubService_GetCheckoutOptions_WireShapeMatchesConsoleDecoder(t *testing.T) {
	svc := newStub(&fakeLedger{}, "BD")

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	require.NoError(t, err)

	raw, err := json.Marshal(opts)
	require.NoError(t, err)

	var payload struct {
		Rails []struct {
			Rail     string `json:"rail"`
			Label    string `json:"label"`
			Currency string `json:"currency"`
			Enabled  *bool  `json:"enabled"`
		} `json:"rails"`
		CreditIncrement *int64 `json:"credit_increment"`
		MinCredits      *int64 `json:"min_credits"`
		MaxCredits      *int64 `json:"max_credits"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))

	require.NotEmpty(t, payload.Rails)
	for _, r := range payload.Rails {
		assert.NotEmpty(t, r.Rail, "console decoder drops a rail item with no rail")
		assert.NotEmpty(t, r.Label, "console decoder drops a rail item with no label")
		assert.NotEmpty(t, r.Currency, "console decoder drops a rail item with no currency")
		require.NotNil(t, r.Enabled, "console decoder drops a rail item with no enabled flag")
		assert.True(t, *r.Enabled, "stub mode completes a checkout on any country-permitted rail")
	}

	require.NotNil(t, payload.CreditIncrement)
	require.NotNil(t, payload.MinCredits)
	require.NotNil(t, payload.MaxCredits)
	assert.Equal(t, payments.CreditIncrement, *payload.CreditIncrement)
	assert.Equal(t, payments.MinPurchaseCredits, *payload.MinCredits)

	// The advertised ceiling must be the most restrictive rail on offer, never
	// looser than what ValidatePurchaseAmount will enforce on initiate. BD
	// offers all three and Stripe is the tightest.
	assert.Equal(t, payments.MaxPurchaseCreditsStripe, *payload.MaxCredits)
	for _, r := range opts.Rails {
		assert.LessOrEqual(t, opts.MaxCredits, r.MaxCredits,
			"advertised ceiling exceeds the %s per-rail ceiling", r.Rail)
	}
}
