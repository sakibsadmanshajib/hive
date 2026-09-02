package executor

// Per-line settlement pricing for the local batch executor (issue #1473).
//
// What this replaces. DefaultCreditPolicy used to charge a flat one credit per
// token and to prefer usage.total_tokens whenever it was positive. Both halves
// were wrong and both were live, because the Phase 15 local executor bypasses
// LiteLLM's managed batch API and runs every line through
// /v1/chat/completions:
//
//   - Flat pricing read no catalog price at all. hive-auto owns every
//     supports_batch route and is pricing_mode upstream_actual with NULL price
//     columns, so the one alias that can reach this path was priced by a
//     formula with no relation to its cost.
//   - total_tokens is not the sum of the two billable classes. A
//     thinking-capable route reports thought tokens inside the total and
//     outside the candidates, so billing the total bills for tokens the
//     customer never received (issue #1472).
//
// What settlement bills on now. Never a token total. For a fixed-price alias,
// prompt_tokens and completion_tokens priced independently at their own
// catalog rates (D-031, credits per million, one round half up at the end).
// For an upstream_actual alias, no token quantity at all: the cost the
// provider reported for that specific generation, at the credit peg and with
// no margin factor (D-064).
// No token class that was previously unbilled starts being billed, which is
// what D-055 requires; only the rate applied to the two classes changes.
//
// Fail closed, batch edition (D-034). By the time this runs the upstream call
// has been made, we have paid for it, and the completion is on its way into
// output.jsonl. Refusing to settle is therefore not the same as refusing to
// serve, because there is nothing left to refuse. So a failure here can never
// mean charging zero:
//
//   - An unreadable cost on a priceable alias settles at that alias's own
//     per-request hold (model_aliases.reservation_estimate_credits),
//     unconfirmed, with a reason naming the exact failure. This mirrors
//     UpstreamActualSettlement, which settles every failed cost read at the
//     full hold. executor.Settle still clamps the batch total at the
//     customer's reservation, so this can never charge past what was held,
//     and it raises the overconsumed flag rather than settling quietly cheap.
//   - An alias with no usable price at all is refused: no charge and no
//     delivery, because there is no defensible figure to charge.
//     routing.SelectRoute already rejects that ALIAS SHAPE with
//     ErrAliasNotPriced before a line runs, since it is static model_aliases
//     configuration checked once per batch.
//   - A fixed-price alias whose provider reported no billable token components
//     is refused the same way, and this one is NOT covered by that guard.
//     SelectRoute validates the catalog row, not the particular response a
//     particular line came back with, and decodeUsage returns nil whenever an
//     otherwise valid 2xx body simply omits its usage object. What makes it
//     unreachable today is narrower and worth stating plainly: hive-auto is
//     the only alias carrying supports_batch and it is upstream_actual, so no
//     fixed-price alias reaches this branch at all. The moment one gains
//     supports_batch the branch goes live, and the trade it makes is
//     deliberate rather than incidental: a delivered completion that omits its
//     usage block is DISCARDED rather than charged an approximate figure. The
//     old code charged a flat 1000 credits for exactly this shape, which is a
//     number nothing derived. Absorbing one upstream call is the fail-closed
//     side of that choice, and quirky but valid upstream shapes are an
//     observed phenomenon on this path, not a hypothetical, so whoever adds a
//     fixed-price batch alias should decide deliberately whether an estimate
//     is worth building rather than inherit this refusal by surprise.
//
// A confident zero cost alongside real tokens settles at the hold rather than
// free, exactly as it does on the sync path, because we cannot tell a
// genuinely free upstream from a broken cost field and July is the precedent
// for which way to err.
//
// Why this arithmetic lives here. The same conversions exist in
// apps/edge-api/internal/inference (upstream_cost.go, pricing.go) and
// apps/edge-api/internal/metering. Go's internal-package visibility makes
// every one of them unimportable from control-plane, which is the same wall
// that produced packages/sanitize for issue #1235. Extracting a shared
// packages/pricing the way sanitize was extracted is the right end state and
// is recommended as a follow-up; it requires editing edge-api, which this
// change deliberately does not touch. Until then the constants below are
// pinned against their sources by TestSettlementConstantsMatchTheMoneyPath so
// the duplication cannot drift silently.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

const (
	// creditsPerUSD mirrors payments.CreditsPerUSD (D-046, 1 USD =
	// 1,000,000,000 credits since the 2026-08-23 rescale). Declared here
	// rather than imported so this package keeps its deliberately small
	// dependency set; the test imports payments and asserts they match.
	creditsPerUSD = 1_000_000_000

	// creditsPerMillion is the unit model_aliases.input_price_credits and
	// output_price_credits are stored in (D-031).
	creditsPerMillion = 1_000_000

	// maxCostLiteralBytes caps the raw numeric literal before big.Rat parses
	// it. JSON puts no limit on digits and this is a money path reachable by
	// an upstream response.
	maxCostLiteralBytes = 64

	// maxChargeableCredits is the largest charge a single batch line may
	// settle at: 10 USD. Refused rather than clamped, so whatever produced an
	// absurd figure stays visible.
	maxChargeableCredits = 10_000_000_000
)

// Cost-read failures. Separate values rather than one generic failure because
// "the field was missing" and "the field said zero" have different causes and
// different fixes, and collapsing them is the specific mistake the sync path
// exists to avoid. None of them ever produces a zero charge.
var (
	errUpstreamCostAbsent      = errors.New("executor: upstream reported no cost")
	errUpstreamCostUnparseable = errors.New("executor: upstream cost is unparseable")
	errUpstreamCostNegative    = errors.New("executor: upstream cost is negative")
	errUpstreamCostZero        = errors.New("executor: upstream cost is zero while tokens were consumed")
	errUpstreamCostImplausible = errors.New("executor: upstream cost is implausibly large")
	// errChargeImplausible is the fixed-price path's equivalent: a computed
	// charge that overflows or exceeds the per-line ceiling. Both paths refuse
	// rather than substitute a number.
	errChargeImplausible = errors.New("executor: computed charge is implausibly large")

	// errLineNotPriceable is the refusal: there is no defensible figure to
	// charge for this line at all. The caller turns it into a failed line
	// rather than a charge.
	errLineNotPriceable = errors.New("executor: line carries no usable price")
)

// LinePrice is one line's settlement decision. The amount and its provenance
// travel on the same value on purpose: a fix that restores the amount while
// emptying the handle is exactly the shape an amount-only assertion misses, so
// there is no way to assert one without the other in scope.
type LinePrice struct {
	// Credits is what this line charges.
	Credits int64
	// Confirmed is true only when the charge came from a figure the upstream
	// itself reported (a readable cost, or token counts on a priced alias).
	// False means the fail-closed hold was charged instead.
	Confirmed bool
	// Reason is a short machine token for the log line, never customer-facing.
	Reason string
	// GenerationID is the upstream's own id for this generation. LiteLLM
	// rewrites the response model field, so this is what lets anyone recover
	// which model the router actually chose long after the fact. Logged, never
	// returned to a customer.
	GenerationID string
}

// upstreamCharge is what a completed generation cost us and what identifies it.
type upstreamCharge struct {
	costUSD      *big.Rat
	generationID string
}

// upstreamCostEnvelope is a private view of the RAW upstream response. Kept
// separate from anything customer-bound so the cost cannot leak by
// construction; packages/sanitize strips these same fields on the way out.
type upstreamCostEnvelope struct {
	ID    string `json:"id"`
	Usage *struct {
		// json.Number, never float64: the literal is preserved as text and
		// handed to big.Rat, so no binary floating point touches a money figure.
		Cost             *json.Number `json:"cost"`
		PromptTokens     int64        `json:"prompt_tokens"`
		CompletionTokens int64        `json:"completion_tokens"`
	} `json:"usage"`
}

// parseUpstreamCost reads the provider-reported cost out of a raw upstream
// chat-completion body. It returns an error for every shape that is not a
// usable positive cost, and never returns a zero charge with a nil error.
func parseUpstreamCost(raw []byte) (upstreamCharge, error) {
	if len(raw) == 0 {
		// No bytes at all is absence, not a malformed payload.
		return upstreamCharge{}, errUpstreamCostAbsent
	}
	var env upstreamCostEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return upstreamCharge{}, fmt.Errorf("%w: %v", errUpstreamCostUnparseable, err)
	}
	charge := upstreamCharge{generationID: env.ID}
	if env.Usage == nil || env.Usage.Cost == nil {
		return charge, errUpstreamCostAbsent
	}
	literal := env.Usage.Cost.String()
	// Length first, before big.Rat ever sees it: the parse is the expensive part.
	if len(literal) > maxCostLiteralBytes {
		return charge, fmt.Errorf("%w: cost literal is %d bytes, limit %d",
			errUpstreamCostUnparseable, len(literal), maxCostLiteralBytes)
	}
	// The byte cap alone does not bound the WORK it was written to bound, which
	// makes its stated purpose and its actual effect differ. Measured in the
	// toolchain container over 200 iterations: 56 nines followed by e999999 is
	// 63 bytes, inside the cap, and expands to a roughly 3.3 million bit
	// integer costing 65 to 130 milliseconds and 2 to 3 megabytes per parse.
	// The only reason that is bounded at all is that Go's math/big caps the
	// exponent at 999999, which is an accident of the standard library rather
	// than a decision of ours, and it should not be the thing standing between
	// this parser and unbounded cost.
	//
	// So the shape is rejected outright rather than merely capped. A USD cost
	// literal has no legitimate reason to carry scientific notation, and
	// big.Rat additionally accepts an "a/b" fraction syntax that JSON numbers
	// never produce, which is a second way a short literal expands enormously.
	if strings.ContainsAny(literal, "eE/") {
		return charge, fmt.Errorf("%w: cost literal %q uses exponent or fraction syntax",
			errUpstreamCostUnparseable, literal)
	}
	cost, ok := new(big.Rat).SetString(literal)
	if !ok {
		return charge, fmt.Errorf("%w: %q", errUpstreamCostUnparseable, literal)
	}
	switch cost.Sign() {
	case -1:
		return charge, errUpstreamCostNegative
	case 0:
		if env.Usage.PromptTokens+env.Usage.CompletionTokens > 0 {
			return charge, errUpstreamCostZero
		}
		return charge, errUpstreamCostAbsent
	}
	charge.costUSD = cost
	return charge, nil
}

// creditsForUpstreamCost converts a provider-reported USD cost into whole
// credits: cost x creditsPerUSD, taken as one exact rational and rounded half
// up exactly once (the same discipline D-031 applies to the per-million path).
// A nonzero cost floors at one credit, so a line that cost real money is never
// settled free.
//
// No margin factor, deliberately. D-064 retired the 1.4 multiplier from every
// settlement path on 2026-09-02 and moved margin to the purchase price
// (D-065); this function mirrors inference.CreditsForUpstreamCost, which no
// longer carries one either.
func creditsForUpstreamCost(costUSD *big.Rat) (int64, error) {
	if costUSD == nil {
		return 0, errUpstreamCostAbsent
	}
	switch costUSD.Sign() {
	case -1:
		return 0, errUpstreamCostNegative
	case 0:
		return 0, errUpstreamCostZero
	}
	scaled := new(big.Rat).Mul(costUSD, new(big.Rat).SetInt64(creditsPerUSD))
	credits, ok := roundHalfUp(scaled)
	if !ok {
		return 0, fmt.Errorf("%w: does not fit in int64", errUpstreamCostImplausible)
	}
	if credits > maxChargeableCredits {
		return 0, fmt.Errorf("%w: %d credits exceeds the %d per-line ceiling",
			errUpstreamCostImplausible, credits, maxChargeableCredits)
	}
	if credits < 1 {
		credits = 1
	}
	return credits, nil
}

// creditsForTokens prices a fixed-price alias's line: prompt tokens at the
// input rate and completion tokens at the output rate, both in credits per
// million, summed FIRST and divided once so two halves cannot round
// independently and drift. A nonzero quantity floors at one credit.
//
// ponytail: prompt tokens all price at the input rate. The batch line's usage
// decode reads no cache-read or cache-write split, so there is no second class
// to price and beginning to bill one would widen the billed set, which D-055
// forbids. If the batch path ever decodes cached-token components, they get
// their own rates then, not a rate invented here.
//
// Negative counts are clamped away rather than allowed to subtract from the
// charge: token counts come from a provider response, so they are external
// input on a money path.
func creditsForTokens(promptTokens, completionTokens int64, pricing catalog.CatalogPricing) (int64, error) {
	promptTokens = max(promptTokens, 0)
	completionTokens = max(completionTokens, 0)
	// Rates are clamped too, not just quantities. A negative price column is a
	// catalog defect rather than a discount, and letting one through would let
	// one component SUBTRACT from another component's charge, which is the
	// only way this function could return less than the work actually cost.
	inputRate := max(derefCredits(pricing.InputPriceCredits), 0)
	outputRate := max(derefCredits(pricing.OutputPriceCredits), 0)

	numerator := new(big.Int).Mul(big.NewInt(promptTokens), big.NewInt(inputRate))
	numerator.Add(numerator, new(big.Int).Mul(big.NewInt(completionTokens), big.NewInt(outputRate)))

	credits, ok := roundHalfUp(new(big.Rat).SetFrac(numerator, big.NewInt(creditsPerMillion)))
	if !ok {
		return 0, fmt.Errorf("%w: does not fit in int64", errChargeImplausible)
	}
	// Both pricing paths take the SAME per-line ceiling and both REFUSE past
	// it. Clamping to the ceiling instead would settle a failed computation as
	// a charge, and a clamped line would be byte-for-byte indistinguishable
	// from a genuine ten dollar line: same amount, same confirmed flag, same
	// catalog_price reason. Nobody could find those lines afterwards to audit
	// or refund them, which makes an unauditable wrong charge worse than a
	// loud one. It is also the only place in this repository where hitting a
	// ceiling would produce a charge: upstream_cost.go refuses on the same
	// condition, and pricing.go's magnitude guard alarms without altering the
	// charge. Neither substitutes a number.
	//
	// Clamping a negative token COUNT to zero above is a different thing and
	// not a precedent for this: that normalises an impossible input into a
	// valid domain before pricing, where this would replace a computed output
	// to hide that pricing failed.
	if credits > maxChargeableCredits {
		return 0, fmt.Errorf("%w: %d credits exceeds the %d per-line ceiling",
			errChargeImplausible, credits, maxChargeableCredits)
	}
	if promptTokens+completionTokens > 0 && credits < 1 {
		credits = 1
	}
	return credits, nil
}

// roundHalfUp resolves an exact NON-NEGATIVE rational to whole credits,
// rounding a remainder of at least half the denominator upward. ok is false
// for a negative input, and when the result does not fit in an int64: big.Int.Int64 is UNDEFINED in that case and
// returns the low 64 bits with the sign reinterpreted, so an oversized charge
// would wrap to a negative and then hit a floor rather than being refused.
func roundHalfUp(value *big.Rat) (int64, bool) {
	// Negative input is refused rather than documented away. QuoRem truncates
	// toward zero, so the half-up bump below would round a negative AWAY from
	// zero and the stated behaviour would simply be false for it. Both callers
	// guarantee a non-negative value today, and "currently dead" is exactly how
	// the missing ceiling above got here, so this refuses instead of trusting
	// that to hold.
	if value.Sign() < 0 {
		return 0, false
	}
	quotient, remainder := new(big.Int).QuoRem(value.Num(), value.Denom(), new(big.Int))
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(value.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, false
	}
	return quotient.Int64(), true
}

// derefCredits coerces an absent price to zero. Only ever correct where the
// caller has already established the alias HAS a fixed price, which is what
// catalog.HasFixedPrice answers: one side at zero is legitimate, both sides
// absent is the case priceLine refuses outright.
func derefCredits(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// Reasoning-token conventions, and why the charge is the same under all of them.
//
// Two upstream conventions exist for where reasoning tokens are counted, and
// they cannot be told apart by model family, only by the numbers:
//
//   - INSIDE completion. prompt + completion == total, and completion_tokens
//     already contains reasoning_tokens. This is OpenAI's o-series convention,
//     and the upstream bills us for those tokens as output.
//   - ALONGSIDE completion. prompt + completion + reasoning == total, and
//     completion_tokens excludes reasoning_tokens. This is Google's thoughts
//     convention, measured live on this pool at prompt 4, completion 1,
//     reasoning 26, total 31.
//
// Settlement charges prompt plus completion in BOTH cases, deliberately:
//
//   - Inside: the reasoning is already part of completion_tokens and the
//     upstream charged us for it as output, so charging it is cost recovery
//     rather than an overcharge. Subtracting it would be a pricing decision,
//     not a defect fix.
//   - Alongside: the reasoning is outside completion_tokens and stays unbilled.
//     Beginning to bill it would widen the set of billed token classes, which
//     D-055 forbids outright. Erring toward the customer is the safe direction
//     and it is the direction issue #1473 exists to restore.
//
// What is never charged, in any convention, is total_tokens. Under "alongside"
// the total contains 26 tokens the customer never received, and charging it is
// the measured 6.2x overcharge this issue was filed for.
//
// explainsReportedTotal reports whether the reported components account for the
// reported total under either convention. False means a third shape nobody has
// characterised, on a money path. The charge does not change (it is still
// prompt plus completion, still never the total); the settlement is labelled
// and logged so an unrecognised shape is visible rather than silent.
func explainsReportedTotal(usage *Usage) bool {
	if usage == nil || usage.TotalTokens == 0 {
		// No total reported means there is no identity to satisfy, not a
		// violated one.
		return true
	}
	if usage.TotalTokens < 0 {
		// A negative total is nonsense rather than an absent one, and folding
		// it into the same bucket would label an impossible shape as
		// explained. It has no charge impact, this is a log label only.
		return false
	}
	// big.Int rather than int64 addition: every field here is upstream
	// controlled, and near-MaxInt64 counts would otherwise wrap and make a
	// violated identity read as satisfied.
	components := new(big.Int).Add(big.NewInt(usage.PromptTokens), big.NewInt(usage.CompletionTokens))
	total := big.NewInt(usage.TotalTokens)
	if components.Cmp(total) == 0 {
		return true
	}
	return new(big.Int).Add(components, big.NewInt(usage.ReasoningTokens)).Cmp(total) == 0
}

// priceLine is the whole settlement decision for one succeeded batch line.
// An error means the line cannot be priced at all and must be refused rather
// than charged; every other outcome returns a LinePrice that is never zero.
func priceLine(pricing catalog.CatalogPricing, usage *Usage, rawUpstreamBody []byte) (LinePrice, error) {
	if pricing.IsUpstreamActual() {
		hold := derefCredits(pricing.ReservationEstimateCredits)
		if hold <= 0 {
			// routing.SelectRoute refuses this shape with ErrAliasNotPriced
			// before a batch runs, so it is unreachable rather than merely
			// unlikely. Refused here too: with no hold there is no fail-closed
			// figure, and inventing one is how a money path starts lying.
			return LinePrice{}, fmt.Errorf("%w: upstream_actual alias carries no reservation estimate", errLineNotPriceable)
		}
		charge, err := parseUpstreamCost(rawUpstreamBody)
		if err == nil {
			var credits int64
			credits, err = creditsForUpstreamCost(charge.costUSD)
			if err == nil {
				return LinePrice{Credits: credits, Confirmed: true, Reason: "upstream_cost", GenerationID: charge.generationID}, nil
			}
		}
		return LinePrice{
			Credits:      hold,
			Confirmed:    false,
			Reason:       costFailureReason(err),
			GenerationID: charge.generationID,
		}, nil
	}

	if !pricing.HasFixedPrice() {
		return LinePrice{}, fmt.Errorf("%w: alias has neither a fixed price nor a variable pricing mode", errLineNotPriceable)
	}
	if usage == nil || usage.PromptTokens+usage.CompletionTokens <= 0 {
		// A fixed-price alias has no reservation estimate by construction, so
		// there is no hold to fall back to and no quantity to price. The old
		// code charged a flat 1000 credits here, which was a fabricated
		// number. Refuse instead.
		//
		// This is a RUNTIME property of one response, not an alias shape, so
		// SelectRoute's ErrAliasNotPriced does not guard it. See the package
		// comment above for why it is nonetheless unreachable today and what
		// changes the day a fixed-price alias gains supports_batch.
		//
		// Zero components folds into the same refusal on purpose, and is not
		// the same question as an empty completion: prompt tokens alone are a
		// billable quantity, so only a response reporting NEITHER lands here.
		// Letting it through would settle a delivered line at zero, which is
		// the free-serve shape of July 2026, and total_tokens is not an escape
		// hatch from it because the total is the very figure #1473 removes.
		return LinePrice{}, fmt.Errorf("%w: fixed-price alias reported no billable token components", errLineNotPriceable)
	}
	credits, err := creditsForTokens(usage.PromptTokens, usage.CompletionTokens, pricing)
	if err != nil {
		// Refused, not clamped, and not charged a substitute. Same decision
		// creditsForUpstreamCost makes on the same condition.
		return LinePrice{}, fmt.Errorf("%w: %v", errLineNotPriceable, err)
	}
	reason := "catalog_price"
	if !explainsReportedTotal(usage) {
		// The charge is unchanged, and is still the components rather than the
		// total. Only the label changes, so a shape neither convention explains
		// shows up in a log instead of settling silently.
		reason = "catalog_price_unexplained_total"
	}
	return LinePrice{Credits: credits, Confirmed: true, Reason: reason}, nil
}

// costFailureReason maps a cost-read failure to a distinct log token. Kept
// apart rather than folded into one "cost_unavailable" because telling them
// apart in a log is the whole point of them being separate errors.
func costFailureReason(err error) string {
	switch {
	case errors.Is(err, errUpstreamCostAbsent):
		return "upstream_cost_absent"
	case errors.Is(err, errUpstreamCostZero):
		return "upstream_cost_zero"
	case errors.Is(err, errUpstreamCostNegative):
		return "upstream_cost_negative"
	case errors.Is(err, errUpstreamCostUnparseable):
		return "upstream_cost_unparseable"
	case errors.Is(err, errUpstreamCostImplausible):
		return "upstream_cost_implausible"
	default:
		return "upstream_cost_error"
	}
}
