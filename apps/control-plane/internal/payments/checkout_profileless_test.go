package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Regression coverage for issue #1386.
//
// An account with no `public.account_profiles` row made the entire checkout
// surface answer 500: GetCheckoutOptions and InitiateCheckout both read the
// full profile only to obtain CountryCode, and both treated profiles.ErrNotFound
// as a server fault. Live on 2026-08-29 that made "Buy credits" unusable on the
// deployed console, and issue #999 counted 14 such accounts on the live
// database.
//
// The country is already optional: the repository COALESCEs a NULL country_code
// to the empty string, and AvailableRails("") is a defined answer. A missing row
// is therefore the same question, and gets the same answer.

func profilelessService(t *testing.T, rails map[Rail]PaymentRail) (*Service, uuid.UUID) {
	t.Helper()
	repo := newStubRepository()
	prof := &stubProfiles{accountProfileErr: profiles.ErrNotFound}
	if rails == nil {
		rails = map[Rail]PaymentRail{}
	}
	svc := buildService(repo, &stubLedger{}, prof, &stubFXProvider{}, rails)
	return svc, uuid.New()
}

func TestGetCheckoutOptions_AccountWithNoProfileRowStillLists(t *testing.T) {
	svc, accountID := profilelessService(t, map[Rail]PaymentRail{
		RailStripe: newStubRail(RailStripe),
	})

	opts, err := svc.GetCheckoutOptions(context.Background(), accountID)
	if err != nil {
		t.Fatalf("expected checkout options for an account with no profile row, got error: %v", err)
	}

	// An unresolved country resolves to the non-BD rail set, never wider.
	if len(opts.Rails) != 1 {
		t.Fatalf("expected exactly the non-BD rail set (1 rail), got %d: %+v", len(opts.Rails), opts.Rails)
	}
	if opts.Rails[0].Rail != RailStripe {
		t.Errorf("expected stripe for an unresolved country, got %s", opts.Rails[0].Rail)
	}

	// Pricing bound: exactly 106 minor units per CreditsPerUSD credits, in USD,
	// with no FX snapshot taken (the FX branch is BD-only).
	if opts.Currency != "USD" {
		t.Errorf("expected USD for an unresolved country, got %q", opts.Currency)
	}
	if opts.PricePerBlockMinor != 106 {
		t.Errorf("expected 106 minor units per block, got %d", opts.PricePerBlockMinor)
	}
	if opts.CreditBlockSize != CreditsPerUSD {
		t.Errorf("expected block size %d, got %d", CreditsPerUSD, opts.CreditBlockSize)
	}
}

func TestGetCheckoutOptions_PropagatesARealProfileFailure(t *testing.T) {
	boom := errors.New("profiles: connection reset")
	repo := newStubRepository()
	prof := &stubProfiles{accountProfileErr: boom}
	svc := buildService(repo, &stubLedger{}, prof, &stubFXProvider{}, map[Rail]PaymentRail{})

	if _, err := svc.GetCheckoutOptions(context.Background(), uuid.New()); !errors.Is(err, boom) {
		t.Fatalf("a genuine repository failure must still fail the request, got %v", err)
	}
}

func TestInitiateCheckout_AccountWithNoProfileRowCannotReachABDRail(t *testing.T) {
	svc, accountID := profilelessService(t, map[Rail]PaymentRail{
		RailBkash: newStubRail(RailBkash),
	})

	_, err := svc.InitiateCheckout(
		context.Background(), accountID, RailBkash, MinPurchaseCredits,
		"https://cp.example.com", "https://console.example.com", uuid.NewString(),
	)
	if err == nil {
		t.Fatal("a BD rail must stay unreachable for an account whose country cannot be resolved")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected a rail-availability refusal, got %v", err)
	}
}
