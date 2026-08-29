package payments

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The purchase floor and the authorization hold are one relationship written as
// two constants, and until issue #1450 nothing compared them. The minimum
// purchase was one US cent while the first chat message a buyer sends takes a
// ten cent hold, so the product sold a quantity it then refused to spend:
// "Your available credit does not cover this request", immediately after paying.
//
// The two numbers cannot be joined by the compiler. MinPurchaseCredits lives in
// apps/control-plane and DefaultHoldText lives in apps/edge-api, separate Go
// modules with no dependency edge between them, and neither should acquire one:
// the money plane importing the data plane (or the reverse) to share a literal
// would be a worse coupling than the drift it prevents.
//
// So the guard is the positional-parse pattern this repository already uses
// wherever a relationship crosses a boundary the compiler cannot: credit_unit_
// rescale_test.go parses the rescale migration's SQL, and catalog_alias_pricing_
// test.go parses the catalog on the routing side. The rule stated there governs
// here too. Digits somewhere in a file is not an assertion; a named symbol
// inside the arithmetic is.
//
// Reading the hold from source on every run is what makes this bidirectional.
// It fails if the minimum falls, and it fails if the hold rises past what the
// minimum covers, which is the half a one-directional constant check misses.

const edgeAPIPricingRelPath = "../../../edge-api/internal/inference/pricing.go"

// Anchored on the declared name and type, so a renamed, retyped, or
// computed-at-runtime DefaultHoldText does not match and the test says so
// instead of quietly measuring nothing.
var defaultHoldTextDecl = regexp.MustCompile(`(?m)^\s*DefaultHoldText\s+int64\s*=\s*([0-9_]+)\b`)

// readDefaultHoldText returns the chat-endpoint hold as edge-api declares it.
//
// Every failure here is fatal rather than skipped. A guard that stops guarding
// because it could not find its input is indistinguishable in a green run from
// a guard that passed, and that shape is exactly what let this defect ship.
func readDefaultHoldText(t *testing.T) int64 {
	t.Helper()

	source, err := os.ReadFile(edgeAPIPricingRelPath)
	if err != nil {
		t.Fatalf("read %s: %v (this guard needs edge-api's hold; if the file moved, move this path with it)", edgeAPIPricingRelPath, err)
	}

	matches := defaultHoldTextDecl.FindAllStringSubmatch(string(source), -1)
	if len(matches) != 1 {
		t.Fatalf("found %d declarations of `DefaultHoldText int64 = <literal>` in %s, want exactly 1; the hold is no longer a plain literal this guard can read, so re-express the relationship rather than deleting the guard", len(matches), edgeAPIPricingRelPath)
	}

	hold, err := strconv.ParseInt(strings.ReplaceAll(matches[0][1], "_", ""), 10, 64)
	if err != nil {
		t.Fatalf("parse DefaultHoldText literal %q: %v", matches[0][1], err)
	}
	if hold <= 0 {
		t.Fatalf("DefaultHoldText = %d, want a positive hold", hold)
	}
	return hold
}

// TestMinimumPurchaseCoversTheChatHoldWithMargin is the guard issue #1450 asks
// for: the smallest purchase the product offers must buy a usable amount of the
// product.
//
// Margin, not parity, is the property. A minimum equal to exactly one hold is
// the same defect with a smaller radius: it funds one message in flight and
// refuses the second concurrent one, and it does not cover a variable-price
// alias at all, because DefaultHoldText is the endpoint FLOOR that
// ReservationCredits raises for hive-auto from the request's own bounds.
func TestMinimumPurchaseCoversTheChatHoldWithMargin(t *testing.T) {
	hold := readDefaultHoldText(t)

	// The mirror is pinned exactly, not merely bounded. The inequality below
	// would still hold for a while as the mirror went stale, and a stale
	// mirror is the state this whole guard exists to make impossible.
	if ChatHoldCredits != hold {
		t.Fatalf("ChatHoldCredits = %d but edge-api declares DefaultHoldText = %d in %s; the mirror has drifted from the hold it mirrors",
			ChatHoldCredits, hold, edgeAPIPricingRelPath)
	}

	if MinPurchaseHoldMultiple < 2 {
		t.Fatalf("MinPurchaseHoldMultiple = %d, want at least 2: a minimum of exactly one hold funds a single message in flight and refuses the next one", MinPurchaseHoldMultiple)
	}

	want := hold * MinPurchaseHoldMultiple
	if MinPurchaseCredits < want {
		t.Fatalf("MinPurchaseCredits = %d, want at least %d (%d x DefaultHoldText %d): a buyer of the minimum is refused on their first request",
			MinPurchaseCredits, want, MinPurchaseHoldMultiple, hold)
	}
}

// TestSuggestedTiersClearTheMinimum holds the customer-visible half. An
// advertised tier below the floor is worse than a permissive floor, because the
// product is actively proposing the amount that will not work. The tiers are
// derived from MinPurchaseCredits so this cannot happen by construction; the
// assertion is on the property rather than on the values, so a future
// hand-written tier list is caught too.
func TestSuggestedTiersClearTheMinimum(t *testing.T) {
	if len(PredefinedTiers) == 0 {
		t.Fatal("PredefinedTiers is empty: the checkout surface offers nothing to suggest")
	}

	previous := int64(0)
	for i, tier := range PredefinedTiers {
		if tier < MinPurchaseCredits {
			t.Errorf("tier[%d] = %d is below the minimum purchase %d, so buying it is refused at checkout or unusable after it", i, tier, MinPurchaseCredits)
		}
		if tier%CreditIncrement != 0 {
			t.Errorf("tier[%d] = %d is not a whole one-cent step (%d), which ValidatePurchaseAmount refuses", i, tier, CreditIncrement)
		}
		if tier <= previous {
			t.Errorf("tier[%d] = %d does not exceed tier[%d] = %d; the suggestions must ascend", i, tier, i-1, previous)
		}
		previous = tier
	}

	if top := PredefinedTiers[len(PredefinedTiers)-1]; top > MaxPurchaseCreditsStripe {
		t.Errorf("top tier %d exceeds the Stripe ceiling %d, so the suggestion cannot be bought on the default rail", top, MaxPurchaseCreditsStripe)
	}
}

// TestValidatePurchaseAmountEnforcesTheFloor keeps the advertised floor and the
// enforced floor the same number.
//
// Before this change nothing enforced a minimum anywhere: MinPurchaseCredits
// was published through GetCheckoutOptions as min_credits and clamped only by
// the console, so a caller reaching InitiateCheckout directly could still buy
// one cent. That is the identical two-uncoupled-numbers defect one layer up,
// and ValidatePurchaseAmount's own doc comment already makes the argument for
// the ceiling that applies unchanged to the floor.
func TestValidatePurchaseAmountEnforcesTheFloor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		credits int64
		ok      bool
	}{
		{"the minimum itself", MinPurchaseCredits, true},
		{"one cent step above the minimum", MinPurchaseCredits + CreditIncrement, true},
		{"one cent step below the minimum", MinPurchaseCredits - CreditIncrement, false},
		{"the old one-cent minimum", CreditIncrement, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePurchaseAmount(tc.credits, RailStripe)
			if tc.ok && err != nil {
				t.Fatalf("want accept, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want reject of %d credits, got nil", tc.credits)
			}
		})
	}
}
