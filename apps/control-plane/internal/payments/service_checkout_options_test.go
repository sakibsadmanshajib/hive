package payments

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
)

// FX-17-03 — Phase 17 per-country pricing primitive on CheckoutOptions.
//
// Internal package_test (white-box) so that we can inject the in-package
// stub types defined alongside `service_test.go` without exporting them.

// TestGetCheckoutOptions_BDAccount_BDTPaisa verifies BD branch resolves
// USD → BDT paisa via FX snapshot using math/big.
//
// Effective rate fixture: 115.500000 (mid 110.00 + 5% fee).
// Expected paisa per CreditsPerUSD-block (= per 1 USD-equiv 100,000 credits):
//
//	paisa_per_block = floor(effectiveRate * 100)
//	                = floor(115.500000 * 100)
//	                = floor(11550)
//	                = 11550
func TestGetCheckoutOptions_BDAccount_BDTPaisa(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "BD", AccountType: "personal"},
	}
	fxProv := &stubFXProvider{
		snap: FXSnapshot{
			BaseCurrency:  "USD",
			QuoteCurrency: "BDT",
			MidRate:       "110.00",
			FeeRate:       "0.05",
			EffectiveRate: "115.500000",
			SourceAPI:     "admin_override",
			FetchedAt:     time.Now(),
			CreatedAt:     time.Now(),
		},
	}
	svc := buildService(repo, led, prof, fxProv, nil)

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetCheckoutOptions: %v", err)
	}
	if opts.Currency != "BDT" {
		t.Errorf("expected Currency=BDT for BD account, got %q", opts.Currency)
	}
	const wantPaisa int64 = 11550
	if opts.PricePerBlockMinor != wantPaisa {
		t.Errorf("expected PricePerBlockMinor=%d (paisa per USD-block), got %d", wantPaisa, opts.PricePerBlockMinor)
	}
	if opts.CreditBlockSize != CreditsPerUSD {
		t.Errorf("expected CreditBlockSize=%d, got %d", CreditsPerUSD, opts.CreditBlockSize)
	}

	// Wire-shape check: marshalled JSON must NOT leak FX rate or USD keys.
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"amount_usd", "price_per_credit_usd", "exchange_rate", "effective_rate", "mid_rate", "fee_rate", "fx_"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("CheckoutOptions wire shape leaks banned key %q\npayload: %s", banned, raw)
		}
	}
}

// TestGetCheckoutOptions_BDAccount_TruncatesViaMathBig confirms the math/big
// integer truncation path: a non-round effective rate truncates correctly.
//
// effectiveRate = 115.557777 → paisa = floor(11555.7777) = 11555
func TestGetCheckoutOptions_BDAccount_TruncatesViaMathBig(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "BD"},
	}
	fxProv := &stubFXProvider{
		snap: FXSnapshot{
			BaseCurrency:  "USD",
			QuoteCurrency: "BDT",
			MidRate:       "110.05",
			FeeRate:       "0.05",
			EffectiveRate: "115.557777",
			SourceAPI:     "admin_override",
			FetchedAt:     time.Now(),
			CreatedAt:     time.Now(),
		},
	}
	svc := buildService(repo, led, prof, fxProv, nil)

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetCheckoutOptions: %v", err)
	}
	const wantPaisa int64 = 11555
	if opts.PricePerBlockMinor != wantPaisa {
		t.Errorf("expected PricePerBlockMinor=%d (truncated paisa), got %d", wantPaisa, opts.PricePerBlockMinor)
	}
}

// TestGetCheckoutOptions_NonBDAccount_USDCents verifies non-BD branch
// returns 100 cents per CreditsPerUSD-block (= 1 USD per USD-block) and
// Currency="USD". FX provider is NOT consulted.
func TestGetCheckoutOptions_NonBDAccount_USDCents(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US", AccountType: "personal"},
	}
	// Empty fxProvider — non-BD path must NOT call it.
	fxProv := &stubFXProvider{}
	svc := buildService(repo, led, prof, fxProv, nil)

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetCheckoutOptions: %v", err)
	}
	if opts.Currency != "USD" {
		t.Errorf("expected Currency=USD for non-BD account, got %q", opts.Currency)
	}
	if opts.PricePerBlockMinor != 100 {
		t.Errorf("expected PricePerBlockMinor=100 cents per USD-block, got %d", opts.PricePerBlockMinor)
	}
	if opts.CreditBlockSize != CreditsPerUSD {
		t.Errorf("expected CreditBlockSize=%d, got %d", CreditsPerUSD, opts.CreditBlockSize)
	}
}

// TestGetCheckoutOptions_WireShape_NoUSDLeak asserts the marshalled JSON
// of CheckoutOptions for BOTH BD and non-BD branches contains neither
// `price_per_credit_usd` nor any other banned FX/USD key. This is the
// black-box complement to TestCheckoutOptionsWireShape_FXZeroLeak in
// service_fx_zero_leak_test.go and protects against regressions where a
// resolved-value field accidentally re-introduces a USD wire key.
func TestGetCheckoutOptions_WireShape_NoUSDLeak(t *testing.T) {
	cases := []struct {
		name        string
		countryCode string
		fx          *stubFXProvider
	}{
		{
			name:        "BD",
			countryCode: "BD",
			fx: &stubFXProvider{snap: FXSnapshot{
				EffectiveRate: "115.500000",
				MidRate:       "110.00",
				FeeRate:       "0.05",
				SourceAPI:     "admin_override",
				FetchedAt:     time.Now(),
				CreatedAt:     time.Now(),
			}},
		},
		{
			name:        "US",
			countryCode: "US",
			fx:          &stubFXProvider{},
		},
	}
	bannedKeys := []string{
		"amount_usd",
		"price_per_credit_usd",
		"exchange_rate",
		"effective_rate",
		"mid_rate",
		"fee_rate",
		"fx_",
		"usd_",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepository()
			svc := buildService(
				repo,
				&stubLedger{},
				&stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: tc.countryCode}},
				tc.fx,
				nil,
			)
			opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
			if err != nil {
				t.Fatalf("GetCheckoutOptions: %v", err)
			}
			raw, err := json.Marshal(opts)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, k := range bannedKeys {
				if strings.Contains(string(raw), k) {
					t.Errorf("[%s] CheckoutOptions JSON leaks banned key %q\npayload: %s", tc.name, k, raw)
				}
			}
			// Required new keys (positive assertions).
			if !strings.Contains(string(raw), `"currency"`) {
				t.Errorf("[%s] CheckoutOptions wire missing required key %q", tc.name, "currency")
			}
			if !strings.Contains(string(raw), `"price_per_block_minor"`) {
				t.Errorf("[%s] CheckoutOptions wire missing required key %q", tc.name, "price_per_block_minor")
			}
			if !strings.Contains(string(raw), `"credit_block_size"`) {
				t.Errorf("[%s] CheckoutOptions wire missing required key %q", tc.name, "credit_block_size")
			}
		})
	}
}
