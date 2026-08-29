package inference

import (
	"encoding/json"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Zero-content guard (issue #1171).
//
// The hive-free alias load-balances heterogeneous members behind one
// litellm_model_name: edge-api dispatches to a pool, never to a member,
// because LiteLLM's router picks whichever deployment answers. Three of its
// four members reason; a reasoning member can spend the caller's entire
// max_tokens on hidden reasoning and answer finish_reason=length with no
// visible content at all, which used to settle as an ordinary full-price
// success (live evidence on #1171: five of six reasoning prompts returned
// empty content and were billed in full).
//
// ZERO-CONTENT GUARD, post-response on the sync path. A chat completion whose
// every choice carries finish_reason=length with no visible output and no tool
// call is retried once against the same pool. If the retry is empty too (or
// the retry itself fails), the request settles fail-closed: capture the
// reservation hold with terminal_usage_confirmed=false, bounded by the
// caller's own completion ceiling (capCaptureAtCeiling, issue #1283), the same
// capture shape UpstreamActualSettlement has always applied to variable-price
// aliases and PR #1220 extends to streams. A loud alarm counter
// (hive_zero_content_captured_total) plus an honest caller-facing flag
// (X-Hive-Upstream-Empty-Content response header) accompany it.
//
// WHAT USED TO LIVE HERE, and why it does not any more. PR #1225 paired the
// guard with pre-dispatch HEADROOM: when route.ReasoningReserveTokens was
// positive, every completion-limit field the caller had set was inflated by
// that reserve before dispatch, on the premise that hidden reasoning would
// burn the reserve while visible content survived inside the caller's own
// budget. The premise is not enforceable. Nothing in an OpenAI-compatible
// request tells an upstream that part of a ceiling is earmarked for hidden
// reasoning, so the model spends the inflated ceiling on whichever it emits;
// and because the rewrite skipped a ceiling the caller had NOT set, its entire
// effect was overriding ceilings callers HAD set. Live on 2026-08-28
// (issue #1283): max_tokens: 8 went upstream as 4104 and came back as 1650
// billed completion tokens. The caller's ceiling now reaches the provider
// untouched, which is also what OpenAI specifies for a reasoning model, where
// reasoning tokens count against the ceiling. provider_routes.
// reasoning_reserve_tokens survives as what gates this guard: it marks a pool
// whose members reason, which is exactly the pool where an empty-length answer
// means a reasoning burn rather than an upstream fault.

const (
	// emptyContentHeader names the flag surfaced to a caller whose sync chat
	// completion came back with no visible output even after one retry had
	// already run. The value records the upstream finish_reason that
	// produced it.
	emptyContentHeader = "X-Hive-Upstream-Empty-Content"

	emptyContentHeaderValue = "length"
)

// isEmptyLengthCompletion reports whether normalized is a chat completion in
// the reasoning-burn shape: at least one choice, every choice finishing with
// finish_reason=length carrying no visible content and no tool call or legacy
// function call. It is the detector behind the sync-path guard.
//
// Deliberately narrow:
//   - finish_reason=stop with empty text is NOT matched. Genuine empty answers
//     exist (the usage_clamp comment calls them legitimate), and a stop-finish
//     means the model chose to end; rewriting that settlement would charge
//     nothing for a delivered response the upstream called complete.
//   - A tool-call message with null content is spec-correct (coerceNullContent
//     leaves it alone for the same reason) and must not trip the guard.
//   - Refusal strings count as visible output: they are generated assistant
//     output the caller can act on, same as chatChoiceTexts treats them.
func isEmptyLengthCompletion(normalized []byte) bool {
	var resp ChatCompletionResponse
	if err := json.Unmarshal(normalized, &resp); err != nil {
		return false
	}
	if len(resp.Choices) == 0 {
		return false
	}
	for _, choice := range resp.Choices {
		if choice.FinishReason == nil || !strings.EqualFold(*choice.FinishReason, "length") {
			return false
		}
		if choice.Message.Content != nil && *choice.Message.Content != "" {
			return false
		}
		if rawFieldPresent(choice.Message.ToolCalls) || rawFieldPresent(choice.Message.FunctionCall) {
			return false
		}
		if choice.Message.Refusal != nil && *choice.Message.Refusal != "" {
			return false
		}
	}
	return true
}

// isZeroContentStream is the streaming half of the same guard (issue #1326).
//
// The sync guard can retry, because on that path emptiness is known before a
// single byte has left the gateway. A stream has already shipped its chunks by
// the time the terminal frame proves there was nothing in them, so there is
// nothing left to retry and nothing honest to say in a contract-shaped frame.
// What is still open at that point is the money: a stream that carried no
// assistant-visible text is not charged for.
//
// THE RULE IS ABOUT THE STREAM, NEVER ABOUT THE SOCKET. Emptiness is decided by
// two facts and nothing else: what the relay DELIVERED, and whether the
// upstream stream COMPLETED. Whether the caller's connection is still open at
// settlement time is not one of them, and an earlier draft of this guard that
// consulted it (a clientGone argument read from reqCtx.Err() inside the
// settlement defer) failed in both directions:
//
//   - A client that closes the moment it receives [DONE] is the NORMAL ending
//     of a blank stream, since a blank stream is exactly what a caller hangs up
//     on. That close cancels r.Context() while the settlement defer is still
//     running, so the clientGone test suppressed the guard precisely in the
//     case the guard exists for, and billed the burn.
//   - A Caddy or Cloudflare reset, an http.Server WriteTimeout, or a graceful
//     shutdown cancels the same context for a reason that has nothing to do
//     with the caller at all. Same wrong charge, no user action anywhere.
//   - A proxy that buffers and does not propagate a real client close promptly
//     leaves the context live, so the same test served a genuine abandonment
//     for free. Lower stakes, opposite direction, same broken discriminator.
//
// StreamCompleted is the discriminator instead, and the relay sets it rather
// than settlement inferring it: a stream that reached its upstream's own end of
// stream is complete however the socket behaved afterwards, and a stream cut
// off mid-flight is not complete however healthy the socket looks. So a
// completed stream with zero visible text is a burn even if the caller has
// already gone, and a truncated stream bills even if the caller is still
// connected, which is the fail-closed direction (D-034).
//
// ZERO VISIBLE CONTENT means exactly that: no assistant-visible text. content
// is what the caller's own client rendered, and nothing else counts. The two
// relays do not accumulate the same fields: AccumulateContent folds
// delta.refusal into content on the chat relay, while the Responses
// translator's currentContent is written only from delta.content, because that
// same builder is what its caller-visible output_text events are emitted from.
// HasVisibleRefusal closes that gap for both relays without changing what
// either one emits. Consequences worth stating rather than leaving to be
// rediscovered:
//
//   - REASONING-ONLY streams are empty. Hidden reasoning costs Hive real money
//     upstream and is exactly the shape this guard exists for: the customer
//     received nothing they can read, so the customer does not pay. The loss is
//     deliberate and lands on Hive, not on the caller.
//   - TOOL-CALL-ONLY streams are NOT empty. A turn that emitted only tool-call
//     frames is a complete, useful response with no text in it, and it bills
//     normally. HasToolCall, not the text, is what carries that.
//   - A stream that never finished, or finished on anything other than
//     "length", is NOT empty for this purpose. finish_reason=stop with no text
//     is an upstream calling a response complete, which the sync guard has
//     always declined to second-guess, and no finish_reason at all means the
//     relay was cut off before anything was known. Both bill (D-034).
//   - A relay that ABORTED (bufio.ErrTooLong on a single oversized upstream
//     line, or a dead upstream connection) is not complete, even when the
//     finish_reason frame had already arrived before the abort. The frame that
//     failed to scan is by definition the one whose contents are unknown, and
//     it could have carried the entire visible answer. StreamCompleted stays
//     false on that path, so the stream bills.
func isZeroContentStream(acc *UsageAccumulator, content string) bool {
	if acc == nil || content != "" {
		return false
	}
	if acc.HasToolCall || acc.HasVisibleRefusal {
		return false
	}
	if !acc.StreamCompleted {
		return false
	}
	return acc.SawFinish && !acc.SawNonLengthFinish
}

// Outcome label values for streamZeroContentAbsorbedCredits. Both series are
// created at registration so a dashboard can tell "no burns" apart from "this
// code never ran"; see RegisterZeroContentMetrics.
const (
	// zeroContentOutcomeReleased: the hold was handed back and the customer
	// was charged nothing. This series IS the absorbed-cost answer.
	zeroContentOutcomeReleased = "released"
	// zeroContentOutcomeReleaseFailed: the burn was detected but the release
	// call did not reach control-plane, so the hold sits until the TTL reaper
	// reclaims it under its own reason. Counted apart so a settlement that
	// failed can never be read as an absorption that landed cleanly.
	zeroContentOutcomeReleaseFailed = "release_failed"
)

// streamZeroContentAbsorbedCredits is the money half of the zero-content
// signal, and the single series an operator reads to answer "how much did we
// absorb yesterday":
//
//	increase(hive_stream_zero_content_absorbed_credits_total{outcome="released"}[1d])
//
// It exists because the stream counter beside it
// (hive_stream_zero_content_released_total) counts events, and no number of
// events converts to credits without a per-request price lookup that no
// durable row carries.
//
// WHAT THE FIGURE IS. The credits the request would have been charged, taken
// from the settlement that had already been computed before the guard fired:
// for a variable-price alias that is the upstream's OWN reported cost, decoded
// by UpstreamActualSettlement out of the terminal usage chunk, and for a
// catalog-priced alias it is the catalog price of the tokens the upstream
// reported burning. Both are computed in int64 credits (1 USD =
// 1,000,000,000 credits) and converted to float64 only here, at the Prometheus
// boundary, which every counter in this package crosses. Nothing reads the
// value back into a charge, so the conversion cannot reach money.
//
// WHY A COUNTER IS THE RECORD RATHER THAN A COLUMN. The release call persists a
// reason and the released hold and nothing else; carrying the amount would put
// it on ReleaseReservationInput and require control-plane's accounting service
// to write it, which is a cross-service contract change and not this PR's to
// make. usage_events.hive_credit_delta is the customer's signed spend
// (negative for spend, and "these are counts, never credits" per
// accounting/service.go), while an absorbed burn is Hive's cost and not the
// customer's, so writing it there would report a charge that never happened.
// Until that contract change lands, this counter plus the per-request
// stream_zero_content log line, which carries absorbed_credits, request_id and
// (for a variable-price alias) the generation_id that identifies the pool
// member, are the operator-side record of the money.
//
// WHAT A RISE MEANS. It is a ROUTING signal, not a billing curiosity. Every
// credit here is inference Hive paid an upstream for and deliberately did not
// bill, because the customer received nothing readable. A rising figure means
// the pool is dispatching work to members that spend the caller's whole ceiling
// on hidden reasoning, and the fix is upstream in routing (drop or
// deprioritize that member), never in settlement.
var streamZeroContentAbsorbedCredits = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hive_stream_zero_content_absorbed_credits_total",
	Help: "Credits Hive absorbed on streams that relayed a well-formed sequence of chunks carrying no assistant-visible text at all and were therefore not billed (issue #1326), by outcome. outcome=released is the absorbed total: sum it over a window to get what the absorption cost, with no join and no price lookup. outcome=release_failed is a burn whose hold release never reached control-plane, so its settlement trail is incomplete and the TTL reaper reclaims the hold under a different reason. Both series exist from process start, so zero reads as zero and an absent series means the recording path itself is broken. A rising released total is a routing signal: the pool is sending work to members that burn the caller's ceiling on hidden reasoning.",
}, []string{"outcome"})

// RegisterZeroContentMetrics registers the streaming zero-content guard's money
// counter and creates both of its series at zero.
//
// The initialisation is the point. A CounterVec emits no series at all until
// its first increment, which makes "nobody burned anything yesterday", "this
// branch was never reached", "ObserveShape stopped being called" and "the
// collector was dropped from registration" byte-identical on a dashboard: the
// silent-absence shape, where a broken pipeline and a healthy quiet day look
// the same. Creating both series here means a zero reads as a genuine zero, and
// only a genuinely broken build makes the metric disappear.
//
// Separate from NewStageMetrics deliberately. That function is the package's
// other registration site and is being edited concurrently by PR #1513; a
// second registration site costs one call in main.go and avoids a guaranteed
// conflict on a shared MustRegister line.
func RegisterZeroContentMetrics(reg prometheus.Registerer) {
	reg.MustRegister(streamZeroContentAbsorbedCredits)
	streamZeroContentAbsorbedCredits.WithLabelValues(zeroContentOutcomeReleased).Add(0)
	streamZeroContentAbsorbedCredits.WithLabelValues(zeroContentOutcomeReleaseFailed).Add(0)
}
