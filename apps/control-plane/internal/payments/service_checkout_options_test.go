package payments

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// FX-17-03 — Phase 17 per-country pricing primitive on CheckoutOptions.
//
// Internal package_test (white-box) so that we can inject the in-package
// stub types defined alongside `service_test.go` without exporting them.

// TestGetCheckoutOptions_BDAccount_BDTPaisa verifies BD branch resolves
// USD → BDT paisa via FX snapshot using math/big.
//
// Effective rate fixture: 115.500000, whatever mid rate and FX fee produced it.
// Expected paisa per CreditsPerUSD-block (= per 1 USD-equivalent of credits;
// the block itself is CreditsPerUSD, 1e9 since the 2026-08-23 rescale). The
// block is priced at 106 cents rather than 100 because the 6 percent purchase
// markup applies to every purchase (D-065), and the FX markup is already inside
// the effective rate:
//
//	paisa_per_block = floor(effectiveRate * 106)
//	                = floor(115.500000 * 106)
//	                = floor(12243)
//	                = 12243
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
	const wantPaisa int64 = 12243
	if opts.PricePerBlockMinor != wantPaisa {
		t.Errorf("expected PricePerBlockMinor=%d (paisa per USD-block), got %d", wantPaisa, opts.PricePerBlockMinor)
	}
	if opts.CreditBlockSize != CreditsPerUSD {
		t.Errorf("expected CreditBlockSize=%d, got %d", CreditsPerUSD, opts.CreditBlockSize)
	}
}

// TestGetCheckoutOptions_BDAccount_TruncatesViaMathBig confirms the math/big
// integer truncation path: a non-round effective rate truncates correctly.
//
// effectiveRate = 115.557777 → paisa = floor(115.557777 * 106)
//              = floor(12249.124362) = 12249
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
	const wantPaisa int64 = 12249
	if opts.PricePerBlockMinor != wantPaisa {
		t.Errorf("expected PricePerBlockMinor=%d (truncated paisa), got %d", wantPaisa, opts.PricePerBlockMinor)
	}
}

// TestGetCheckoutOptions_NonBDAccount_USDCents verifies non-BD branch
// returns 106 cents per CreditsPerUSD-block (1 USD of credit value at the peg,
// plus the 6 percent purchase markup) and Currency="USD". FX provider is NOT
// consulted, which is also what keeps the FX markup off a USD price.
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
	if opts.PricePerBlockMinor != 106 {
		t.Errorf("expected PricePerBlockMinor=106 cents per USD-block, got %d", opts.PricePerBlockMinor)
	}
	if opts.CreditBlockSize != CreditsPerUSD {
		t.Errorf("expected CreditBlockSize=%d, got %d", CreditsPerUSD, opts.CreditBlockSize)
	}
}

// TestGetCheckoutOptions_WireShape_RequiredKeys asserts the marshalled JSON
// of CheckoutOptions for BOTH BD and non-BD branches carries the fields the
// console depends on to render checkout pricing.
func TestGetCheckoutOptions_WireShape_RequiredKeys(t *testing.T) {
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
