package payments

import (
	"math/big"
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
// Reading the hold from source on every run is what makes this bidirectional,
// and the mirror is pinned exactly rather than bounded, so it is stronger than
// the inequality alone: the minimum falling fails it, and ANY change to the
// hold in either direction fails it too. Whoever next touches DefaultHoldText
// should expect this test to stop them and to have to decide the floor
// deliberately, which is the point.

const (
	edgeAPIPricingRelPath      = "../../../edge-api/internal/inference/pricing.go"
	edgeAPIUpstreamCostRelPath = "../../../edge-api/internal/inference/upstream_cost.go"
)

// readEdgeConst returns the value of a plain integer constant declared in an
// edge-api source file.
//
// The pattern is anchored at BOTH ends of the initializer, so the captured
// digits have to be the whole value and not merely its first token. A renamed,
// retyped or computed declaration therefore fails to match rather than being
// half-read: an unanchored tail would match `DefaultHoldText int64 = 100_000_000
// * 20`, capture the first operand, and pass silently against a hold twenty
// times what it measured, which is the exact silent-false-pass this guard
// exists to refuse. Only trailing spaces and a line comment are tolerated.
//
// Every failure is fatal rather than skipped. A guard that stops guarding
// because it could not find its input is indistinguishable in a green run from
// a guard that passed, and that shape is what let this defect ship.
func readEdgeConst(t *testing.T, path, name string) int64 {
	t.Helper()

	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (this guard needs edge-api's constants; if the file moved, move this path with it)", path, err)
	}

	decl := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(name) + `[ \t]+(?:int64[ \t]+)?=[ \t]*([0-9_]+)[ \t]*(?://[^\n]*)?$`)
	matches := decl.FindAllStringSubmatch(string(source), -1)
	if len(matches) != 1 {
		t.Fatalf("found %d declarations of `%s = <literal>` in %s, want exactly 1; it is no longer a plain literal this guard can read, so re-express the relationship rather than deleting the guard", len(matches), name, path)
	}

	value, err := strconv.ParseInt(strings.ReplaceAll(matches[0][1], "_", ""), 10, 64)
	if err != nil {
		t.Fatalf("parse %s literal %q: %v", name, matches[0][1], err)
	}
	if value <= 0 {
		t.Fatalf("%s = %d, want a positive value", name, value)
	}
	return value
}

// smallestVariablePriceHold recomputes, from edge-api's own constants, the
// SMALLEST hold a variable-price alias such as hive-auto can take.
//
// That is the hold for an empty body which sets no max_tokens, so the whole
// figure is the completion cap priced at the ceiling rate and carried through
// the margin, exactly as variablePriceRequestHold and CreditsForUpstreamCost do
// it (apps/edge-api/internal/inference/pricing.go, upstream_cost.go). Every
// real request holds at least this. Rationals throughout, because the source
// arithmetic is exact and a float here would introduce a disagreement the
// guard would then be measuring instead of the constants.
func smallestVariablePriceHold(t *testing.T) int64 {
	t.Helper()

	completionCap := readEdgeConst(t, edgeAPIPricingRelPath, "VariablePriceMaxCompletionTokens")
	completionRate := readEdgeConst(t, edgeAPIPricingRelPath, "VariablePriceCompletionCeilingUSD")
	marginNum := readEdgeConst(t, edgeAPIUpstreamCostRelPath, "MarginNumerator")
	marginDen := readEdgeConst(t, edgeAPIUpstreamCostRelPath, "MarginDenominator")

	// tokens * rate / 1e6 USD, times the margin, times credits per USD.
	credits := new(big.Rat).SetInt64(completionCap)
	credits.Mul(credits, big.NewRat(completionRate, 1_000_000))
	credits.Mul(credits, big.NewRat(marginNum, marginDen))
	credits.Mul(credits, new(big.Rat).SetInt64(CreditsPerUSD))

	// Truncate rather than round, so this stays a lower bound on the real hold
	// and the assertion below can never fail on a rounding artefact.
	return new(big.Int).Quo(credits.Num(), credits.Denom()).Int64()
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
	hold := readEdgeConst(t, edgeAPIPricingRelPath, "DefaultHoldText")

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

// TestMinimumPurchaseClearsTheRailMinimum asserts the second of the two reasons
// the floor is the size it is. Without it the surviving mutation is
// MinPurchaseHoldMultiple = 2: mirror exact, multiple at least two, floor equal
// to two holds, tiers derived, every other assertion green, at a $0.20 minimum
// that Stripe itself would refuse. A justification that lives only in a comment
// is not a guard.
func TestMinimumPurchaseClearsTheRailMinimum(t *testing.T) {
	if MinPurchaseCredits < StripeMinimumChargeCredits {
		t.Fatalf("MinPurchaseCredits = %d is below Stripe's own minimum charge %d, so the smallest purchase on offer is one the default rail rejects",
			MinPurchaseCredits, StripeMinimumChargeCredits)
	}
}

// TestMinimumPurchaseCoversAVariablePriceHold asserts the third reason, and it
// is the one that decides the multiple.
//
// DefaultHoldText is a per-endpoint FLOOR, not the hold. On a variable-price
// alias such as hive-auto, ReservationCredits sizes the hold from the request's
// own bounds and it lands far above the flat figure: an ordinary turn that sets
// no max_tokens holds against the full completion cap. So a floor derived only
// from the flat hold could be two holds and still not fund one real message on
// the product's default alias, which is the defect this change exists to close
// rather than to rename.
//
// The bound is recomputed from edge-api's own constants on every run, so it
// also catches the mutation that raises the completion ceiling rate or the
// margin: both raise every hive-auto hold, and neither touches a number this
// package can see.
//
// The margin asserted is two of those holds. That is deliberately weaker than
// the ten flat holds the constant is set to, because it is the floor of a real
// hold rather than a typical one, and a guard should fail on the property being
// gone rather than on the number being merely re-tuned.
func TestMinimumPurchaseCoversAVariablePriceHold(t *testing.T) {
	smallest := smallestVariablePriceHold(t)

	const holdsRequired = 2
	if want := smallest * holdsRequired; MinPurchaseCredits < want {
		t.Fatalf("MinPurchaseCredits = %d, want at least %d (%d x the smallest variable-price hold %d): a buyer of the minimum cannot fund an ordinary turn on a variable-price alias",
			MinPurchaseCredits, want, holdsRequired, smallest)
	}
}

// TestSuggestedTiersClearTheMinimum holds the customer-visible half. An
// advertised tier below the floor is worse than a permissive floor, because the
// product is actively proposing the amount that will not work. The tiers are
// derived from MinPurchaseCredits so this cannot happen by construction; the
// assertion is on the property rather than on the values, so a future
// hand-written tier list is caught too.
func TestSuggestedTiersClearTheMinimum(t *testing.T) {
	// Shape, not values. Dropping the old exact-cent pin left nothing
	// constraining the list, and a one-element list holding exactly the floor
	// would have satisfied every other assertion here while the customer-visible
	// suggestions silently collapsed to a single button.
	const minTiers = 3
	if len(PredefinedTiers) < minTiers {
		t.Fatalf("PredefinedTiers has %d entries, want at least %d: the checkout surface has to offer a real choice", len(PredefinedTiers), minTiers)
	}
	if top := PredefinedTiers[len(PredefinedTiers)-1]; top < 5*MinPurchaseCredits {
		t.Errorf("top tier %d is under 5x the floor %d, so the suggestions no longer span a useful range", top, MinPurchaseCredits)
	}

	previous := int64(0)
	for i, tier := range PredefinedTiers {
		if tier < MinPurchaseCredits {
			t.Errorf("tier[%d] = %d is below the minimum purchase %d, so buying it is refused at checkout or unusable after it", i, tier, MinPurchaseCredits)
		}
		if tier%CreditIncrement != 0 {
			t.Errorf("tier[%d] = %d is not a whole one-cent step (%d), which ValidatePurchaseAmount refuses", i, tier, CreditIncrement)
		}
		if i > 0 && tier <= previous {
			t.Errorf("tier[%d] = %d does not exceed tier[%d] = %d; the suggestions must ascend", i, tier, i-1, previous)
		}
		previous = tier
	}

	if top := PredefinedTiers[len(PredefinedTiers)-1]; top > MaxPurchaseCreditsStripe {
		t.Errorf("top tier %d exceeds the Stripe ceiling %d, so the suggestion cannot be bought on the default rail", top, MaxPurchaseCreditsStripe)
	}
}

// TestTheFloorFitsUnderEveryRailCeiling closes the same shape one axis over.
// The floor is global and the ceilings are per rail, so they are once again two
// independent numbers, and nothing compared them either. Tighten any
// MaxPurchaseCredits constant below the floor and that rail advertises
// min_credits above max_credits, which the console decoder rejects as an
// incoherent range, leaving the modal blaming the server for a range the server
// chose. Latent today, since the smallest ceiling is a hundred times the floor.
func TestTheFloorFitsUnderEveryRailCeiling(t *testing.T) {
	for _, rail := range []Rail{RailStripe, RailBkash, RailSSLCommerz} {
		t.Run(string(rail), func(t *testing.T) {
			ceiling := maxCreditsForRail(rail)
			if ceiling < MinPurchaseCredits {
				t.Fatalf("%s ceiling %d is below the purchase floor %d: this rail would advertise a minimum above its own maximum", rail, ceiling, MinPurchaseCredits)
			}
		})
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
