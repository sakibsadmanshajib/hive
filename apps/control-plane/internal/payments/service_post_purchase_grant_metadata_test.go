package payments

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestPostPurchaseGrant_LedgerMetadataCarriesAuditLinkage asserts the
// metadata map passed to LedgerGranter.GrantCredits from PostPurchaseGrant
// preserves the audit linkage (`payment_intent_id`) needed to reconstruct
// the FX snapshot internally, for both BD (FX snapshot present) and non-BD
// (no FX snapshot) intents.
func TestPostPurchaseGrant_LedgerMetadataCarriesAuditLinkage(t *testing.T) {
	t.Parallel()

	fxSnapID := uuid.New()
	cases := []struct {
		name   string
		intent PaymentIntent
	}{
		{
			name: "BD_bkash_with_FX_snapshot",
			intent: PaymentIntent{
				ID:            uuid.New(),
				AccountID:     uuid.New(),
				Rail:          RailBkash,
				Status:        IntentStatusCompleted,
				Credits:       100_000,
				AmountUSD:     100,
				AmountLocal:   12_000_00,
				LocalCurrency: "BDT",
				FXSnapshotID:  &fxSnapID,
				TaxTreatment:  "vat_inclusive",
			},
		},
		{
			name: "non_BD_stripe_no_FX_snapshot",
			intent: PaymentIntent{
				ID:            uuid.New(),
				AccountID:     uuid.New(),
				Rail:          RailStripe,
				Status:        IntentStatusCompleted,
				Credits:       100_000,
				AmountUSD:     100,
				AmountLocal:   100,
				LocalCurrency: "USD",
				FXSnapshotID:  nil,
				TaxTreatment:  "vat_exclusive",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			led := &stubLedger{}
			svc := buildService(newStubRepository(), led, &stubProfiles{}, &stubFXProvider{}, nil)

			if err := svc.PostPurchaseGrant(context.Background(), tc.intent); err != nil {
				t.Fatalf("PostPurchaseGrant: %v", err)
			}
			if got := led.callCount(); got != 1 {
				t.Fatalf("expected exactly 1 ledger grant call, got %d", got)
			}

			led.mu.Lock()
			metadata := led.calls[0].metadata
			led.mu.Unlock()

			// Required positive shape — payment_intent_id is the only
			// audit linkage; without it FX snapshot becomes orphaned in
			// the audit chain.
			pid, ok := metadata["payment_intent_id"].(string)
			if !ok || pid == "" {
				t.Errorf("ledger grant metadata missing payment_intent_id; got %v", metadata["payment_intent_id"])
			}
			if pid != tc.intent.ID.String() {
				t.Errorf("payment_intent_id mismatch: want %s got %s", tc.intent.ID, pid)
			}
		})
	}
}
