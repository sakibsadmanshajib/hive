package inference

import (
	"encoding/json"
	"log"
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
		if toolCallPresent(choice.Message.ToolCalls) || toolCallPresent(choice.Message.FunctionCall) {
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
	if acc == nil {
		return false
	}
	return isZeroContent(acc.Shape(""), content)
}

// Shape projects everything an accumulator observed about delivery onto the
// form the guard reads, for a relay outside this package that accumulates
// through ObserveShape but settles through ChatSettlementCredits (issue #1526).
// The caller supplies the surface label and, if its own notion of completion
// differs from StreamCompleted, overwrites Completed after.
func (a *UsageAccumulator) Shape(surface string) DeliveryShape {
	return DeliveryShape{
		Surface:            surface,
		HasToolCall:        a.HasToolCall,
		HasVisibleRefusal:  a.HasVisibleRefusal,
		Completed:          a.StreamCompleted,
		SawFinish:          a.SawFinish,
		SawNonLengthFinish: a.SawNonLengthFinish,
	}
}

// DeliveryShape is the evidence isZeroContent decides on, in the one form every
// settling surface can produce (issue #1526).
//
// It exists because the guard above reads a *UsageAccumulator, and the
// accumulator belongs to this package's own SSE relay. The Open WebUI session
// chat relay and both halves of /v1/rag/chat settle through
// ChatSettlementCredits with their own decoders and never build one, so the
// only way for them to reach the same predicate was to name the facts it needs
// separately from the type that happened to hold them. A second predicate
// written per surface is how two rules that are supposed to agree drift apart.
//
// Surface is the label the absorbed-credits counter is broken down by, so a
// rise can be attributed to the surface that produced it. It is never part of
// the emptiness decision.
type DeliveryShape struct {
	Surface            string
	HasToolCall        bool
	HasVisibleRefusal  bool
	Completed          bool
	SawFinish          bool
	SawNonLengthFinish bool
}

// ObserveFinishReason folds one finish_reason into the shape, from a relayed
// stream chunk or from a choice of a whole response body. Empty means the
// choice carried none, which is not a finish at all.
func (s *DeliveryShape) ObserveFinishReason(reason string) {
	if reason == "" {
		return
	}
	s.SawFinish = true
	if !isLengthFinish(reason) {
		s.SawNonLengthFinish = true
	}
}

// isLengthFinish is the single spelling of the reasoning-burn finish reason.
// Both the accumulator and DeliveryShape fold through it so the two cannot
// disagree about what "ran out of ceiling" looks like on the wire.
func isLengthFinish(reason string) bool {
	return strings.EqualFold(reason, "length")
}

// isZeroContent is the predicate itself, stated once for every surface.
//
// COMPLETED ON A NON-STREAMING SETTLEMENT. "The stream completed" has no
// meaning for a response that never streamed, and forcing the streaming test
// onto one would make the guard unreachable there: nothing sets a completion
// flag on a body that arrived whole. The property the streaming flag stands in
// for is "everything this response was going to say has already been said", and
// a fully decoded response body satisfies that by construction, since the read
// and the decode both had to succeed before any settlement ran. So a
// non-streaming caller passes Completed: true and emptiness is decided on the
// content and the finish reasons alone, which is exactly what the sync guard
// (isEmptyLengthCompletion) has always done: it reads choices and consults no
// notion of completion anywhere.
func isZeroContent(shape DeliveryShape, content string) bool {
	if content != "" {
		return false
	}
	if shape.HasToolCall || shape.HasVisibleRefusal {
		return false
	}
	if !shape.Completed {
		return false
	}
	return shape.SawFinish && !shape.SawNonLengthFinish
}

// ApplyZeroContentGuard runs the guard over a settlement that has already been
// priced, whichever pricing arm priced it (issue #1538).
//
// WHY IT IS A STEP RATHER THAN A BRANCH OF ONE SETTLEMENT FUNCTION. Session
// chat and settleChat both fork on the alias's pricing mode before settling: a
// catalog-priced alias goes to ChatSettlementCredits, and an alias with no
// catalog price goes to UpstreamActualSettlement, whose Delivered is true on
// any successful cost read. The guard added for #1526 lived inside the first
// of those, so a reasoning burn on hive-auto, the only upstream_actual alias in
// the live catalog, was still charged the cost the upstream reported for tokens
// the customer never saw. This is the shape settleStream has always had: the
// guard applied last, to whatever outcome the pricing branch reached, rather
// than woven into either arm.
//
// It does NOT belong inside UpstreamActualSettlement. That function answers a
// different question, what this generation cost, and it answers it for the
// API-key streaming path too, which already applies its own guard afterwards.
// Putting a delivery test inside a cost reader would give that path two guards
// and this one a cost reader that silently declines charges.
//
// A COST THAT COULD NOT BE READ IS STILL RELEASED. UpstreamActualSettlement is
// fail-closed: an absent, unparseable or confident-zero cost settles at the
// full hold rather than at nothing (D-034). That rule is about not giving work
// away when the PRICE is unknown; this guard is about not charging for work the
// customer never received, which is known either way. So a burn releases
// whatever the cost read did, and absorbed_credits then carries the hold, which
// is exactly what settleStream records on the same combination.
//
// credits and delivered come back unchanged when the guard does not fire.
// zeroContent is the caller's release reason, so a burn is distinguishable in
// the ledger from a provider that died or a customer who hung up.
func ApplyZeroContentGuard(aliasID string, shape DeliveryShape, content string,
	credits int64, delivered bool, promptTokens, completionTokens int64) (int64, bool, bool) {
	if !delivered || !isZeroContent(shape, content) {
		return credits, delivered, false
	}
	// Counted here, from the figure the pricing arm already computed: this is
	// the only point at which the burn's price exists at all, since every
	// caller discards it the moment delivered comes back false.
	chatZeroContentAbsorbedCredits.WithLabelValues(shape.Surface).Add(float64(credits))
	log.Printf("inference: chat_zero_content surface=%s alias=%s absorbed_credits=%d upstream_prompt_tokens=%d upstream_completion_tokens=%d: the turn returned no assistant-visible text, releasing the hold instead of charging (#1526, #1538)",
		shape.Surface, aliasID, credits, promptTokens, completionTokens)
	// Zero rather than the priced figure. No caller charges on a false
	// delivered, and this way one that forgot to check could only ever serve
	// the burn free, which is the direction this guard already decided on.
	return 0, false, true
}

// Surface label values for chatZeroContentAbsorbedCredits. Every one of them is
// created at registration, so a dashboard can tell "no burns on this surface"
// apart from "this surface never reached the guard at all".
const (
	// ZeroContentSurfaceSessionChat is the Open WebUI session chat relay
	// (internal/chat), which is the surface the product demo runs on.
	ZeroContentSurfaceSessionChat = "session_chat"
	// ZeroContentSurfaceRAGStream is the streaming half of /v1/rag/chat.
	ZeroContentSurfaceRAGStream = "rag_stream"
	// ZeroContentSurfaceRAGSync is the non-streaming half of /v1/rag/chat.
	ZeroContentSurfaceRAGSync = "rag_sync"
)

// chatZeroContentAbsorbedCredits is the money signal for the three surfaces
// that settle through ChatSettlementCredits, and the sibling of
// streamZeroContentAbsorbedCredits above:
//
//	increase(hive_chat_zero_content_absorbed_credits_total[1d])
//
// WHAT THE FIGURE IS. The credits the request would have been charged, taken
// from whichever pricing arm priced it: for a catalog-priced alias, the
// alias's own price applied to the tokens the upstream reported burning; for a
// variable-price alias (#1538), the upstream's OWN reported cost at the peg,
// or the hold when that cost could not be read, since that is what
// UpstreamActualSettlement would have charged. Computed in int64 credits and
// converted to float64 only here, at the Prometheus boundary. Nothing reads the
// value back into a charge.
//
// WHY IT KEYS ON THE GUARD'S OWN VERDICT. The counter has to be able to fire on
// every path it claims to cover, and the way that goes wrong is keying it on a
// quantity one of the paths never has: hive_usage_reasoning_tokens_unbilled_total
// could not increment on /v1/rag/chat at all, because that handler dropped
// completion_tokens_details before the counter's branch was reached, so the
// dashboard would have read clean forever while the loss continued (issue
// #1472). This one increments at the moment the guard fires, inside the
// function all three surfaces call, from credits the same call has already
// computed. There is no per-surface precondition left that could be false.
//
// WHAT IT COUNTS, PRECISELY. Credits DECLINED at settlement, not holds proven
// returned. The release is the caller's own deferred call and this function
// cannot observe its outcome; a release that fails is already an ERROR log from
// sessionbilling.Settlement.Release ("release failed, hold may be stranded"),
// which is where that half of the story stays. The streaming guard can split
// released from release_failed because it performs the release itself.
var chatZeroContentAbsorbedCredits = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hive_chat_zero_content_absorbed_credits_total",
	Help: "Credits Hive absorbed on session chat and RAG chat turns that returned no assistant-visible text at all and were therefore not billed (issues #1526 and #1538), by surface, on both catalog-priced and variable-price aliases. Sum it over a window to get what the absorption cost, with no join and no price lookup. All three surface series exist from process start, so zero reads as zero and an absent series means the recording path itself is broken. A rising total is a routing signal: the pool is sending work to members that burn the caller's ceiling on hidden reasoning. It counts credits declined at settlement; a hold whose release then failed is an ERROR log from the release call, not a series here.",
}, []string{"surface"})

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
	reg.MustRegister(streamZeroContentAbsorbedCredits, chatZeroContentAbsorbedCredits)
	streamZeroContentAbsorbedCredits.WithLabelValues(zeroContentOutcomeReleased).Add(0)
	streamZeroContentAbsorbedCredits.WithLabelValues(zeroContentOutcomeReleaseFailed).Add(0)
	for _, surface := range []string{
		ZeroContentSurfaceSessionChat,
		ZeroContentSurfaceRAGStream,
		ZeroContentSurfaceRAGSync,
	} {
		chatZeroContentAbsorbedCredits.WithLabelValues(surface).Add(0)
	}
}
