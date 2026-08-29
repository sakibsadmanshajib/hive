package payments

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Regression coverage for the second half of issue #1386: the rails payload the
// control-plane sends cannot be decoded by the console, so the checkout modal
// renders no payment method even once the 500 is gone.

// TestCheckoutOptionsWireShape pins the payload to the shape the console's
// decoder actually requires. decodeCheckoutRail
// (apps/web-console/lib/control-plane/client.ts) drops any rail item missing
// `rail`, `currency`, `label` or `enabled`, so a payload without them arrives at
// the checkout modal as an empty rail list and renders no payment method at all.
// The console's own fixture is written in the shape the client wants rather than
// the shape the server sends, so it cannot catch this (issue #797).
func TestCheckoutOptionsWireShapeMatchesConsoleDecoder(t *testing.T) {
	repo := newStubRepository()
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "US"}}
	svc := buildService(repo, &stubLedger{}, prof, &stubFXProvider{}, map[Rail]PaymentRail{
		RailStripe: newStubRail(RailStripe),
	})

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}

	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{
		"rails", "predefined_tiers", "price_per_block_minor", "credit_block_size",
		"currency", "credit_increment", "min_credits", "max_credits",
	} {
		if _, ok := payload[key]; !ok {
			t.Errorf("checkout options payload is missing %q, which the console reads", key)
		}
	}

	// The decoder rejects a falsy value, not only an absent key, so an empty
	// string drops the item just as surely as a missing field does. Assert the
	// values, not just the shape.
	var railItems []struct {
		Rail     string `json:"rail"`
		Label    string `json:"label"`
		Currency string `json:"currency"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(payload["rails"], &railItems); err != nil {
		t.Fatalf("unmarshal rails: %v", err)
	}
	if len(railItems) == 0 {
		t.Fatal("expected at least one rail item")
	}
	for i, item := range railItems {
		if item.Rail == "" {
			t.Errorf("rail item %d has an empty rail, so the console decoder drops it", i)
		}
		if item.Label == "" {
			t.Errorf("rail item %d has an empty label, so the console decoder drops it", i)
		}
		if item.Currency == "" {
			t.Errorf("rail item %d has an empty currency, so the console decoder drops it", i)
		}
		if item.Enabled == nil {
			t.Errorf("rail item %d has no enabled flag, so the console decoder drops it", i)
		}
	}
}

// A rail the deployment cannot actually execute must not advertise itself as
// usable. Rails are registered only when their credentials are present in the
// environment, so `enabled` is that registration and nothing else.
func TestCheckoutOptions_EnabledTracksRailRegistration(t *testing.T) {
	repo := newStubRepository()
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "US"}}

	withRail := buildService(repo, &stubLedger{}, prof, &stubFXProvider{}, map[Rail]PaymentRail{
		RailStripe: newStubRail(RailStripe),
	})
	opts, err := withRail.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}
	if len(opts.Rails) != 1 || !opts.Rails[0].Enabled {
		t.Errorf("a registered rail must be advertised as enabled, got %+v", opts.Rails)
	}

	withoutRail := buildService(repo, &stubLedger{}, prof, &stubFXProvider{}, map[Rail]PaymentRail{})
	opts, err = withoutRail.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}
	if len(opts.Rails) != 1 || opts.Rails[0].Enabled {
		t.Errorf("an unregistered rail must not be advertised as enabled, got %+v", opts.Rails)
	}
}

// The advertised ceiling must never be looser than what ValidatePurchaseAmount
// enforces for the rail the payer eventually picks. The console carries one
// top-level bound, so that bound is the minimum across the rails on offer.
func TestCheckoutOptions_MaxCreditsIsTheMostRestrictiveAvailableRail(t *testing.T) {
	repo := newStubRepository()
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "BD"}}
	fx := &stubFXProvider{snap: FXSnapshot{EffectiveRate: "115.500000"}}
	svc := buildService(repo, &stubLedger{}, prof, fx, map[Rail]PaymentRail{
		RailStripe:     newStubRail(RailStripe),
		RailBkash:      newStubRail(RailBkash),
		RailSSLCommerz: newStubRail(RailSSLCommerz),
	})

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}

	// Both halves matter. Zero would satisfy "never exceeds a rail ceiling"
	// while advertising a cap nobody can buy under, so pin the exact value too.
	// BD offers all three rails and Stripe is the tightest at 100 USD.
	if opts.MaxCredits != MaxPurchaseCreditsStripe {
		t.Errorf("expected the most restrictive ceiling %d, got %d", MaxPurchaseCreditsStripe, opts.MaxCredits)
	}
	for _, rail := range opts.Rails {
		if opts.MaxCredits > rail.MaxCredits {
			t.Errorf("advertised max %d exceeds the %s ceiling %d", opts.MaxCredits, rail.Rail, rail.MaxCredits)
		}
	}
	if opts.MinCredits != MinPurchaseCredits {
		t.Errorf("expected min %d, got %d", MinPurchaseCredits, opts.MinCredits)
	}
	if opts.CreditIncrement != CreditIncrement {
		t.Errorf("expected increment %d, got %d", CreditIncrement, opts.CreditIncrement)
	}
}

// A rail nobody can select must not set the bound. On a deployment with bKash
// registered and Stripe not, counting the disabled Stripe entry would cap the
// console at Stripe's 100.00 USD ceiling while the only selectable rail accepts
// 300.00 USD (CodeRabbit, PR #1393).
func TestCheckoutOptions_MaxCreditsIgnoresUnselectableRails(t *testing.T) {
	repo := newStubRepository()
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "BD"}}
	fx := &stubFXProvider{snap: FXSnapshot{EffectiveRate: "115.500000"}}
	svc := buildService(repo, &stubLedger{}, prof, fx, map[Rail]PaymentRail{
		RailBkash: newStubRail(RailBkash),
	})

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}
	if opts.MaxCredits != MaxPurchaseCreditsBkash {
		t.Errorf("expected the only selectable rail's ceiling %d, got %d", MaxPurchaseCreditsBkash, opts.MaxCredits)
	}
	for _, rail := range opts.Rails {
		if rail.Enabled && opts.MaxCredits > rail.MaxCredits {
			t.Errorf("advertised max %d exceeds the selectable %s ceiling %d", opts.MaxCredits, rail.Rail, rail.MaxCredits)
		}
	}
}

// A deployment with no usable rail advertises no purchasable amount rather than
// inheriting a ceiling from a rail that cannot run.
func TestCheckoutOptions_NoSelectableRailAdvertisesNoCeiling(t *testing.T) {
	repo := newStubRepository()
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "US"}}
	svc := buildService(repo, &stubLedger{}, prof, &stubFXProvider{}, map[Rail]PaymentRail{})

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}
	if opts.MaxCredits != 0 {
		t.Errorf("expected no advertised ceiling with no selectable rail, got %d", opts.MaxCredits)
	}
}
