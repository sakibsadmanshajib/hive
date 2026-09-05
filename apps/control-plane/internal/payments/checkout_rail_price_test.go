package payments

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Issue #1737. The checkout modal has to show the payer what the purchase will
// cost in the currency they are about to pay in, and the figure it shows has to
// be the figure the rail charges.
//
// Two things stood between the console and that figure.
//
// The price was published PER ACCOUNT while it is charged PER RAIL. A BD
// account is offered bKash, SSLCommerz and Stripe (AvailableRails), and
// InitiateCheckout branches on isBDRail, not on the account's country: the two
// local rails charge taka, Stripe charges dollars. One per-account price cannot
// be right for both, so a taka figure rendered against a Stripe checkout would
// have been wrong by the entire exchange rate.
//
// The price was also published PRE-TRUNCATED, as whole minor units per
// CreditsPerUSD block, and the console multiplied that by the number of blocks.
// Exact in dollars. Not exact in taka once D-066 made the effective rate carry
// three decimals, because the fraction of a paisa dropped from the block price
// is dropped again on every block.

// quoteMinor is what the console does with the published ratio: floor the exact
// product for the quantity the payer typed. Written here in big.Int so the test
// exercises the arithmetic contract rather than a Go copy of the TypeScript.
func quoteMinor(credits int64, opt RailOption) int64 {
	product := new(big.Int).Mul(big.NewInt(credits), big.NewInt(opt.PriceMinorNumerator))
	return new(big.Int).Quo(product, big.NewInt(opt.PriceCreditsDenominator)).Int64()
}

func TestCheckoutOptions_QuotedRailPriceEqualsTheChargedAmount(t *testing.T) {
	// Three decimals in the effective rate is the D-066 shape: a mid rate of
	// 127 marked up by 2.5 percent is 130.175, which is exactly what the old
	// whole-paisa block price could not represent.
	const effectiveRate = "130.175000"

	repo := newStubRepository()
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "BD"}}
	fx := &stubFXProvider{snap: FXSnapshot{EffectiveRate: effectiveRate}}
	svc := buildService(repo, &stubLedger{}, prof, fx, map[Rail]PaymentRail{
		RailStripe:     newStubRail(RailStripe),
		RailBkash:      newStubRail(RailBkash),
		RailSSLCommerz: newStubRail(RailSSLCommerz),
	})

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}
	if len(opts.Rails) != 3 {
		t.Fatalf("expected three rails for a BD account, got %d", len(opts.Rails))
	}

	// Quantities that between them cover the floor, a plain multiple, a
	// non-round multiple, and the largest block count the console can offer.
	quantities := []int64{
		MinPurchaseCredits,
		15 * CreditIncrement,
		20 * CreditsPerUSD,
		100 * CreditsPerUSD,
	}

	for _, opt := range opts.Rails {
		rate := ""
		wantCurrency := "USD"
		if isBDRail(opt.Rail) {
			rate = effectiveRate
			wantCurrency = "BDT"
		}
		if opt.Currency != wantCurrency {
			t.Errorf("%s settles in %s but advertises %s", opt.Rail, wantCurrency, opt.Currency)
		}
		if opt.PriceCreditsDenominator <= 0 {
			t.Fatalf("%s published a denominator of %d, which cannot be divided by",
				opt.Rail, opt.PriceCreditsDenominator)
		}
		for _, credits := range quantities {
			charged, err := PriceForCredits(credits, rate)
			if err != nil {
				t.Fatalf("price %d credits on %s: %v", credits, opt.Rail, err)
			}
			want := charged.USDCents
			if isBDRail(opt.Rail) {
				want = charged.LocalMinor
			}
			if got := quoteMinor(credits, opt); got != want {
				t.Errorf("%s quotes %d minor units for %d credits, charges %d (off by %d)",
					opt.Rail, got, credits, want, got-want)
			}
		}
	}
}

// The published ratio has to survive JSON's only number type. JavaScript reads
// every JSON number as a float64, so a numerator or denominator past 2^53 - 1
// arrives at the console already rounded and prices the purchase wrong with
// nothing to indicate it. Refusing to answer is the honest failure; quoting a
// silently rounded price is not.
func TestCheckoutOptions_PublishedRatioIsExactInJavaScript(t *testing.T) {
	const maxSafeInteger = int64(9007199254740991)

	repo := newStubRepository()
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "BD"}}
	fx := &stubFXProvider{snap: FXSnapshot{EffectiveRate: "130.175000"}}
	svc := buildService(repo, &stubLedger{}, prof, fx, map[Rail]PaymentRail{
		RailBkash: newStubRail(RailBkash),
	})

	opts, err := svc.GetCheckoutOptions(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("get checkout options: %v", err)
	}
	for _, opt := range opts.Rails {
		if opt.PriceMinorNumerator <= 0 || opt.PriceMinorNumerator > maxSafeInteger {
			t.Errorf("%s numerator %d is outside the range JavaScript reads exactly",
				opt.Rail, opt.PriceMinorNumerator)
		}
		if opt.PriceCreditsDenominator <= 0 || opt.PriceCreditsDenominator > maxSafeInteger {
			t.Errorf("%s denominator %d is outside the range JavaScript reads exactly",
				opt.Rail, opt.PriceCreditsDenominator)
		}
	}
}

// A taka rail priced without an FX rate would quote the dollar figure with a
// taka symbol in front of it, which is the same class of defect as quoting a
// per-account currency for a per-rail charge, only 130 times worse. Fail closed.
func TestNewRailOption_LocalRailWithoutARateIsRefused(t *testing.T) {
	if _, err := NewRailOption(RailBkash, true, ""); err == nil {
		t.Fatal("expected a local-currency rail with no effective rate to be refused")
	}
	if _, err := NewRailOption(RailStripe, true, ""); err != nil {
		t.Fatalf("a USD rail needs no rate, got %v", err)
	}
}

// PriceRatePerCredit is published to the console; PriceForCredits is what the
// customer is charged. They are two expressions of the order of operations in
// purchase_price.go's header, and this is what stops them drifting apart.
func TestQuotedRateReproducesTheChargedAmount(t *testing.T) {
	rates := []string{"", "130.175000", "115.500000", "1.000000", "127.000000"}
	quantities := []int64{
		0,
		CreditIncrement,
		MinPurchaseCredits,
		7 * CreditIncrement,
		20 * CreditsPerUSD,
		MaxPurchaseCreditsSSLCommerz,
	}

	for _, rate := range rates {
		perCredit, err := PriceRatePerCredit(rate)
		if err != nil {
			t.Fatalf("rate per credit for %q: %v", rate, err)
		}
		for _, credits := range quantities {
			charged, err := PriceForCredits(credits, rate)
			if err != nil {
				t.Fatalf("price %d credits at %q: %v", credits, rate, err)
			}
			want := charged.USDCents
			if rate != "" {
				want = charged.LocalMinor
			}
			product := new(big.Rat).Mul(perCredit, new(big.Rat).SetInt64(credits))
			got := new(big.Int).Quo(product.Num(), product.Denom()).Int64()
			if got != want {
				t.Errorf("rate %q quotes %d for %d credits, charge is %d", rate, got, credits, want)
			}
		}
	}
}
