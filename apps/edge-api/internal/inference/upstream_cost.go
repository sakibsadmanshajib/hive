package inference

// Actual-cost settlement for a variable-price alias.
//
// A router alias (openrouter/auto-beta) picks a different upstream model per
// request, so there is no catalog price to charge against. What there IS, when
// usage accounting is switched on for the route, is the cost the upstream
// reports for that specific generation. This file turns that reported cost into
// a credit charge, and refuses, loudly, to turn anything else into one.
//
// ── Do not take a cost from a proxy-computed field ─────────────────────────
//
// LiteLLM sits between us and OpenRouter and offers an `x-litellm-response-cost`
// header that looks exactly like the answer. It is not. When the proxy has no
// price entry for a model it falls back to its own static price map SILENTLY.
// Measured on 2026-08-22 against our pinned v1.77.7-stable with a fake upstream
// reporting a known cost of 0.0123456: the header said 0.0105, which is the
// proxy's own $3/$15-per-million guess for a router model it cannot price. The
// specific numbers are version-dependent (by v1.83.14 the header returns the
// provider figure on the sync path), which is exactly why the rule below is
// written as a rule and not as a version note:
//
//	Never take a cost from a proxy-computed field when a provider-reported one
//	is available. The proxy falls back to its own price map without saying so,
//	and a router model has no entry in it.
//
// A related trap from the same family, same measurement session: on a STREAMING
// request v1.83.14 returns `x-litellm-response-cost-original: 0.0`. A confident
// zero is worse than an absent value, because an absent value can be caught and
// a zero bills nothing. That is the shape that served this gateway free for
// three days in July, which is why the parser below reports "zero" and "absent"
// as two DIFFERENT errors and neither of them settles at zero.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// Errors from reading a reported upstream cost. They are separate values, not
// one generic failure, because they mean different things operationally and
// because collapsing "the field was missing" into "the field said zero" is the
// specific mistake this package exists to avoid. Every one of them is
// fail-closed at the call site; none of them ever produces a zero charge.
var (
	// ErrUpstreamCostAbsent: no usage object, or no cost field on it. The
	// usual cause is a proxy that dropped the field in transit rather than an
	// upstream that charged nothing.
	ErrUpstreamCostAbsent = errors.New("inference: upstream reported no cost")
	// ErrUpstreamCostUnparseable: a cost field that is not a number we can
	// read exactly.
	ErrUpstreamCostUnparseable = errors.New("inference: upstream cost is unparseable")
	// ErrUpstreamCostNegative: a negative cost. Nonsense on its face, and
	// allowing it would let an upstream response reduce a charge.
	ErrUpstreamCostNegative = errors.New("inference: upstream cost is negative")
	// ErrUpstreamCostZero: the field is present and says exactly zero while
	// tokens were consumed. Kept distinct from Absent deliberately. It is
	// treated as fail-closed rather than free because we cannot tell a
	// genuinely free upstream from a confident-zero defect, and July is the
	// precedent for which way to err. If free routing turns out to be common
	// in practice this becomes a catalog-level allowance, not a silent
	// default.
	ErrUpstreamCostZero = errors.New("inference: upstream cost is zero while tokens were consumed")
)

// MarginNumerator / MarginDenominator express the 1.4 margin EXACTLY, as a
// rational. Not 1.4 the float: this multiplies a money figure, and the repo
// rule is math/big everywhere near a charge.
//
// Until now this margin has only ever been applied by hand, at migration
// authoring time, when someone multiplied a provider list price by 1.4 and
// wrote the result into model_aliases (D-032). Billing at actual cost is the
// first time it has to exist at RUNTIME, so this is a new constant rather than
// a reused one; there was no runtime constant to reuse.
const (
	MarginNumerator   = 7
	MarginDenominator = 5
)

// CreditsPerUSD mirrors payments.CreditsPerUSD (1 USD = 100,000 credits).
// Duplicated rather than imported: payments lives under control-plane's
// internal/ tree in a different module, so Go's own visibility rules make it
// unimportable from edge-api. TestCreditsPerUSDMatchesPaymentsPackage guards
// the duplication against drift.
const CreditsPerUSD = 100_000

// UpstreamCharge is what a completed generation cost us and what identifies it.
type UpstreamCharge struct {
	// CostUSD is the provider-reported cost of this generation, exact.
	CostUSD *big.Rat
	// GenerationID is the upstream's own id for the generation (OpenRouter
	// returns `gen-...`). This is the audit handle: it is what lets anyone
	// recover WHICH model the router actually picked, long after the fact,
	// without us having to carry the model name through a proxy that rewrites
	// it. Recorded internally, never returned to a customer.
	GenerationID string
	// Provider is the upstream the router resolved to, when the response says
	// so. Coarser than the model, internal only, and frequently absent.
	Provider string
}

// upstreamCostEnvelope is a private view of the RAW upstream response. It is
// deliberately NOT part of UsageResponse: the normalize* functions unmarshal
// the response into their typed structs and re-marshal them straight to the
// customer, so a cost field living on UsageResponse would be serialised onto a
// customer-bound body. Parsing separately means our upstream cost cannot leak
// by construction rather than by remembering to strip it.
type upstreamCostEnvelope struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Usage    *struct {
		// json.Number, never float64: the literal is preserved as text and
		// handed to big.Rat, so no binary floating point ever touches a money
		// figure.
		Cost             *json.Number `json:"cost"`
		PromptTokens     int64        `json:"prompt_tokens"`
		CompletionTokens int64        `json:"completion_tokens"`
	} `json:"usage"`
}

// ParseUpstreamCost reads the provider-reported cost out of a raw upstream
// response body (sync) or a raw terminal SSE chunk (streaming).
//
// It returns an error for every shape that is not a usable positive cost, and
// the caller must treat all of them as fail-closed. It never returns a zero
// charge and a nil error together.
func ParseUpstreamCost(raw []byte) (UpstreamCharge, error) {
	var env upstreamCostEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return UpstreamCharge{}, fmt.Errorf("%w: %v", ErrUpstreamCostUnparseable, err)
	}

	charge := UpstreamCharge{GenerationID: env.ID, Provider: env.Provider}

	if env.Usage == nil || env.Usage.Cost == nil {
		return charge, ErrUpstreamCostAbsent
	}

	cost, ok := new(big.Rat).SetString(env.Usage.Cost.String())
	if !ok {
		return charge, fmt.Errorf("%w: %q", ErrUpstreamCostUnparseable, env.Usage.Cost.String())
	}

	switch cost.Sign() {
	case -1:
		return charge, ErrUpstreamCostNegative
	case 0:
		// Zero with no tokens is "nothing happened", which the caller already
		// handles as a release rather than a charge. Zero WITH tokens is the
		// confident-zero shape, and it is refused.
		if env.Usage.PromptTokens+env.Usage.CompletionTokens > 0 {
			return charge, ErrUpstreamCostZero
		}
		return charge, ErrUpstreamCostAbsent
	}

	charge.CostUSD = cost
	return charge, nil
}

// CreditsForUpstreamCost converts a provider-reported USD cost into whole
// credits at the standard margin: cost x 7/5 x 100000, summed as one exact
// rational and rounded half up exactly once, the same rounding discipline
// metering.ChargeCredits applies to the per-million path (D-031).
//
// math/big throughout. A nonzero cost floors at one credit, so a request that
// cost us real money is never settled free.
func CreditsForUpstreamCost(costUSD *big.Rat) (int64, error) {
	if costUSD == nil {
		return 0, ErrUpstreamCostAbsent
	}
	if costUSD.Sign() < 0 {
		return 0, ErrUpstreamCostNegative
	}
	if costUSD.Sign() == 0 {
		return 0, ErrUpstreamCostZero
	}

	scaled := new(big.Rat).Mul(costUSD, big.NewRat(MarginNumerator*CreditsPerUSD, MarginDenominator))

	quotient, remainder := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	// Round half up: a remainder at least half the denominator bumps by one.
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	credits := quotient.Int64()
	if credits < 1 {
		credits = 1
	}
	return credits, nil
}
