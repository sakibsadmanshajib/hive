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

	"github.com/sakibsadmanshajib/hive/packages/sanitize"
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
	// ErrUpstreamCostImplausible: a cost so large it cannot be a real charge.
	// Refused rather than clamped, because silently capping an absurd figure
	// would hide whatever produced it.
	ErrUpstreamCostImplausible = errors.New("inference: upstream cost is implausibly large")
)

// maxCostLiteralBytes caps the raw numeric literal before it is handed to
// big.Rat. json.Number only guarantees JSON number syntax, and JSON puts no
// limit on digits, so an upstream (or anything able to answer as one) could
// send a multi-megabyte literal and big.Rat would faithfully build the exact
// rational for it. That is CPU spent inside a request, on a money path, at the
// caller's choosing. No genuine USD cost needs anywhere near this many
// characters.
const maxCostLiteralBytes = 64

// maxChargeableCredits is the largest charge a single request may settle at.
// One request cannot plausibly cost more than 10,000,000,000 credits (10 USD)
// given the request bounds enforced before dispatch, and refusing past that is
// what stops a wrong or hostile figure becoming a real ledger entry. It also
// keeps the conversion inside int64 by construction, so the Int64 check below
// is a belt-and-braces assertion rather than the only line of defence.
const maxChargeableCredits = 10_000_000_000

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

// CreditsPerUSD mirrors payments.CreditsPerUSD (1 USD = 1,000,000,000
// credits, since the 2026-08-23 credit unit rescale; migration
// 20260823_40_credit_unit_rescale_billion.sql rescaled stored data to match).
// Duplicated rather than imported: payments lives under control-plane's
// internal/ tree in a different module, so Go's own visibility rules make it
// unimportable from edge-api. TestCreditsPerUSDMatchesPaymentsPackage guards
// the duplication against drift.
const CreditsPerUSD = 1_000_000_000

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
}

// upstreamCostEnvelope is a private view of the RAW upstream response. It is
// deliberately NOT part of UsageResponse: the normalize* functions unmarshal
// the response into their typed structs and re-marshal them straight to the
// customer, so a cost field living on UsageResponse would be serialised onto a
// customer-bound body. Parsing separately means our upstream cost cannot leak
// by construction rather than by remembering to strip it.
// The upstream's `provider` field is deliberately NOT decoded. It is redundant,
// because the generation id recovers both the provider and the exact model that
// `provider` alone cannot name, and tools/lint-no-client-cost-fields.mjs forbids
// any provider-named JSON struct tag as a structural guard against provider
// identity reaching a customer, matching on raw file text so a comment quoting
// the tag trips it too. Not decoding the field at all is a smaller and safer
// answer than an allowlist entry arguing that this particular struct is private.
type upstreamCostEnvelope struct {
	ID    string `json:"id"`
	Usage *struct {
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
	// No bytes at all is "the frame never arrived", which is absence, not a
	// malformed payload. json.Unmarshal reports empty input as a syntax error,
	// which would file a stream that ended before its usage frame under
	// `unparseable` and lose the distinction this file is built around.
	if len(raw) == 0 {
		return UpstreamCharge{}, ErrUpstreamCostAbsent
	}

	var env upstreamCostEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return UpstreamCharge{}, fmt.Errorf("%w: %v", ErrUpstreamCostUnparseable, err)
	}

	charge := UpstreamCharge{GenerationID: env.ID}

	if env.Usage == nil || env.Usage.Cost == nil {
		return charge, ErrUpstreamCostAbsent
	}

	literal := env.Usage.Cost.String()
	// Length first, before big.Rat ever sees it: the parse itself is the
	// expensive part, so checking afterwards would be too late.
	if len(literal) > maxCostLiteralBytes {
		return charge, fmt.Errorf("%w: cost literal is %d bytes, limit %d",
			ErrUpstreamCostUnparseable, len(literal), maxCostLiteralBytes)
	}
	cost, ok := new(big.Rat).SetString(literal)
	if !ok {
		return charge, fmt.Errorf("%w: %q", ErrUpstreamCostUnparseable, literal)
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

// SanitizeVariablePriceFrame strips the fields an upstream adds that must
// never reach a customer (cost, provider identity), rewrites the model to
// the alias, and replaces the upstream id with the caller's mintedID.
//
// Name kept despite now running unconditionally on every alias, fixed-price
// included (security review finding, PR #1222): cost-field stripping is a
// no-op on a frame that has none, so one path serves every pricing model
// rather than carrying a second, unsanitized one for fixed-price traffic.
//
// Two callers, both a raw-line SSE relay with no typed-struct protection:
// executeStreaming's fallback (a chunk the typed decode above it could not
// parse) and apps/edge-api/internal/chat's dispatch handler, which relays
// every line through this map-based path rather than a typed one at all.
//
// mintedID must be the SAME value the caller mints once per stream and
// reuses on every other chunk (mintCompletionID), never a fresh id per call:
// this function sanitizes one frame at a time with no memory of the frames
// before it, so a caller that minted a fresh id here would break the
// id-stability contract the moment this path fires mid-stream.
//
// ok is false when the frame cannot be parsed. The caller must then DROP the
// frame rather than forward it, because an unparseable frame is exactly the one
// whose contents are unknown.
//
// Thin wrapper: the strip/rewrite logic itself moved to packages/sanitize
// (issue #1235) so apps/control-plane's local batch executor can sanitize
// its own upstream response bodies the same way without duplicating this
// function. Behavior is unchanged, as are both callers: stream.go's
// executeStreaming fallback in this package, and
// apps/edge-api/internal/chat/dispatch.go's handler in a different one,
// which imports this exported function.
func SanitizeVariablePriceFrame(payload []byte, aliasID, mintedID string) ([]byte, bool) {
	return sanitize.VariablePriceFrame(payload, aliasID, mintedID)
}

// CreditsForUpstreamCost converts a provider-reported USD cost into whole
// credits at the standard margin: cost x 7/5 x CreditsPerUSD (1e9 since the
// 2026-08-23 rescale), summed as one exact
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

	// Int64() is UNDEFINED when the value does not fit: it returns the low 64
	// bits with the sign reinterpreted, so an oversized charge wraps rather
	// than saturating. A wrap to a negative then hit the floor below and became
	// a charge of one credit, flagged confirmed, which is a failed cost read
	// settling as very nearly free. Check before converting, and refuse.
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("%w: does not fit in int64", ErrUpstreamCostImplausible)
	}
	credits := quotient.Int64()
	if credits > maxChargeableCredits {
		return 0, fmt.Errorf("%w: %d credits exceeds the %d per-request ceiling",
			ErrUpstreamCostImplausible, credits, maxChargeableCredits)
	}
	if credits < 1 {
		credits = 1
	}
	return credits, nil
}
