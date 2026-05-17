package payments

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// FX-17 adversarial-review regression — PR #137 follow-up (2026-05-14).
//
// Before this fix, PostPurchaseGrant for a BD payment intent attached
// `fx_snapshot_id` to the ledger entry metadata. Ledger entries are
// serialized verbatim to the customer on GET /api/v1/accounts/current/
// ledger/entries — the `fx_*` key would land on a BD customer surface
// and violate the FX/USD zero-leak contract (Phase 17 / FX-17-09).
//
// This test asserts the metadata map passed to LedgerGranter.GrantCredits
// from PostPurchaseGrant carries none of the FX/USD tripwire keys, and
// that the audit linkage (`payment_intent_id`) is preserved as the only
// supported way to reconstruct the FX snapshot internally.
func TestPostPurchaseGrant_NoFXKeyInLedgerMetadata(t *testing.T) {
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

			// Banned-key sweep — both direct map key check and serialized
			// JSON substring check (matches the customer wire path).
			banned := []string{
				"fx_snapshot_id",
				"amount_usd",
				"exchange_rate",
				"effective_rate",
				"mid_rate",
				"fee_rate",
			}
			for _, k := range banned {
				if _, ok := metadata[k]; ok {
					t.Errorf("ledger grant metadata leaks banned key %q (direct map)", k)
				}
			}
			for k := range metadata {
				if strings.HasPrefix(k, "fx_") || strings.HasPrefix(k, "usd_") {
					t.Errorf("ledger grant metadata leaks banned-prefix key %q", k)
				}
			}

			raw, err := json.Marshal(metadata)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}
			for _, k := range banned {
				if strings.Contains(string(raw), k) {
					t.Errorf("ledger grant metadata JSON contains banned token %q\npayload: %s", k, raw)
				}
			}

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
