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


// railNamed returns the option for one rail, so a test can assert against the
// rail it means rather than a positional index.
func railNamed(t *testing.T, opts *CheckoutOptions, want Rail) RailOption {
	t.Helper()
	for _, opt := range opts.Rails {
		if opt.Rail == want {
			return opt
		}
	}
	t.Fatalf("rail %s is not on offer: %+v", want, opts.Rails)
	return RailOption{}
}

// TestGetCheckoutOptions_BDAccount_BDTPaisa verifies the BD rails resolve
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
	bkash := railNamed(t, opts, RailBkash)
	if bkash.Currency != "BDT" {
		t.Errorf("expected bKash to settle in BDT, got %q", bkash.Currency)
	}
	const wantPaisa int64 = 12243
	if got := quoteMinor(CreditsPerUSD, bkash); got != wantPaisa {
		t.Errorf("expected %d paisa for %d credits on bKash, got %d", wantPaisa, CreditsPerUSD, got)
	}

	// Stripe is on offer to the same BD account and charges dollars, so it must
	// not carry the taka price. This is the half of issue #1737 that a
	// per-account price got wrong by the entire exchange rate.
	stripe := railNamed(t, opts, RailStripe)
	if stripe.Currency != "USD" {
		t.Errorf("expected Stripe to settle in USD even for a BD account, got %q", stripe.Currency)
	}
	if got := quoteMinor(CreditsPerUSD, stripe); got != 106 {
		t.Errorf("expected 106 cents for %d credits on Stripe, got %d", CreditsPerUSD, got)
	}
}

// TestGetCheckoutOptions_BDAccount_TruncatesViaMathBig confirms the math/big
// integer truncation path: a non-round effective rate truncates correctly, and
// truncates ONCE, against the quantity being bought.
//
// effectiveRate = 115.557777, so one CreditsPerUSD block is
// floor(115.557777 * 106) = floor(12249.124362) = 12249 paisa, and twenty of
// them are floor(244982.48724) = 244982 paisa, NOT 20 * 12249 = 244980. The
// second figure is what the console used to render, because it was handed the
// already-truncated block price and multiplied it (issue #1737).
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
	bkash := railNamed(t, opts, RailBkash)
	if got := quoteMinor(CreditsPerUSD, bkash); got != 12249 {
		t.Errorf("expected 12249 paisa for one block, got %d", got)
	}
	if got := quoteMinor(20*CreditsPerUSD, bkash); got != 244982 {
		t.Errorf("expected 244982 paisa for twenty blocks, got %d", got)
	}
	charged, err := PriceForCredits(20*CreditsPerUSD, "115.557777")
	if err != nil {
		t.Fatalf("price twenty blocks: %v", err)
	}
	if charged.LocalMinor != 244982 {
		t.Fatalf("the charge itself moved: expected 244982 paisa, got %d", charged.LocalMinor)
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
	stripe := railNamed(t, opts, RailStripe)
	if stripe.Currency != "USD" {
		t.Errorf("expected Stripe to settle in USD for a non-BD account, got %q", stripe.Currency)
	}
	if got := quoteMinor(CreditsPerUSD, stripe); got != 106 {
		t.Errorf("expected 106 cents for %d credits, got %d", CreditsPerUSD, got)
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
			for _, key := range []string{
				"currency", "price_minor_numerator", "price_credits_denominator",
			} {
				if !strings.Contains(string(raw), `"`+key+`"`) {
					t.Errorf("[%s] CheckoutOptions wire missing required key %q", tc.name, key)
				}
			}
		})
	}
}
