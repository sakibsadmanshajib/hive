package inference

import (
	"encoding/json"
	"log"
	"strings"
	"unicode/utf8"
)

// bytesPerToken is the divisor estimateCompletionTokens uses. It is calibrated
// to UNDER-count real tokenization for every writing system, because the number
// it produces is charged to a customer for usage nobody measured (issue #673).
//
// Unit: UTF-8 bytes, not characters. Every tokenizer our providers serve is a
// byte-level BPE, so a token is always at least one byte ("the token sequence
// is shorter than the bytes corresponding to the original text", tiktoken
// README, github.com/openai/tiktoken), while character count is no bound in
// either direction: measured emoji cost 1.17 tokens per character, so a rune
// count can sit below the truth by any margin. Byte length also tracks real
// tokenization far more evenly across scripts than character count does,
// because a script the vocabulary covers poorly degrades toward one token per
// byte. Measured on the eight scripts in token_estimate_scripts_test.go, bytes
// per token spans 2.6 to 9.0 (3.5x) while characters per token spans 0.9 to 7.1
// (7.9x). Calibrating a rune divisor the same way this one is calibrated (9,
// clearing the sparsest script) would charge English 79 percent of its real
// usage and Japanese 13 percent of theirs, so the customer's writing system
// would decide their discount.
//
// What byte length does NOT give is a free pass for the divisor. The structural
// bound is only one token per byte; nothing makes real text cost a token per 12
// bytes. Repeated punctuation blows straight through it, because the vocabulary
// carries long single tokens for it: measured worst case across o200k_base,
// cl100k_base and o200k_harmony, a run of spaces reaches 128 real bytes per
// token and a run of dashes 64, so counting those runs byte for byte
// over-charged a plain horizontal rule by 6.58x. That is what runCollapsible
// below is for, and the calibration of 12 holds only for text once those runs
// are collapsed.
//
// Value: 12, from measurement, not from a rule of thumb. Real bytes per token
// for representative prose (tiktoken 0.13.0, o200k_base) is 7.1 for English,
// 7.5 for Bengali, 5.4 for Arabic, 4.1 for Chinese, 3.4 for Japanese, 2.6 for
// emoji and 9.0 for Devanagari, the sparsest of the eight. 12 clears that
// sparsest case by about 33 percent, so the estimate lands at 21 to 79 percent
// of real usage across all eight rather than above it.
//
// The 4 this replaced was the average byte-per-token ratio the tiktoken README
// quotes ("on average, in practice, each token corresponds to about 4 bytes").
// An average is the wrong statistic for a figure that must never exceed the
// truth: measured against o200k_base it overcharged six of the eight scripts,
// English by 1.77x, Bengali by 1.87x and Devanagari by 2.36x.
//
// ponytail: one global divisor, recalibrate if we onboard a tokenizer
// materially more efficient than o200k_base on Devanagari (the thinnest margin
// here at 0.79 of real). A per-script divisor would tighten the 21-to-79 band
// but needs a rune classifier and re-measurement per script, and every error it
// would remove is currently in the customer's favour.
const bytesPerToken = 12

// estimateCompletionTokens approximates the token count of a piece of text for
// settlement paths where the provider reported no usable usage: a missing usage
// block (issue #636) or completion_tokens=0 on a non-empty message.
//
// It errs low by construction, and that direction is deliberate: the estimate is
// billed, so any error has to favour the customer. See bytesPerToken for the
// measured margins, and the tests in token_estimate_scripts_test.go for the
// bound this must keep holding.
//
// Runs of a character the vocabulary compresses hard are counted at one byte
// per bytesPerToken bytes rather than byte for byte, so a run stays
// proportional to its real cost instead of either dominating the estimate or
// vanishing from it. See runCollapsible for the measured membership and both
// failure modes it is pinned between.
//
// The result is not a lower bound for every conceivable input: BPE can compress
// a long repeated word into one 17-byte token, and an alternating pair of
// separators the vocabulary pairs up is not a run, so pathological text can
// still estimate above its true count. That alternation case has a measured
// ceiling: sweeping every two-rune alternation over the collapsible members plus
// '|' at 8,000 repetitions, the worst is "=-" and "+-" at 16 real bytes per
// token, estimated at 1.33x, and completing the member set did not move it
// because an alternation is never collapsed either way. The hold clamp in
// control-plane's finalizeLocked (accounting/service.go) remains the backstop
// that keeps an unconfirmed charge inside the authorization the customer
// already granted.
func estimateCompletionTokens(text string) int64 {
	if text == "" {
		return 0
	}
	n := int64((collapsedByteLen(text) + bytesPerToken - 1) / bytesPerToken)
	if n < 1 {
		return 1
	}
	return n
}

// runCollapsible reports whether a run of r repeated is compressed hard enough
// by real tokenizers that counting the run at full byte length would
// over-charge. Membership is measured, never intuited: it is exactly the runes
// whose real bytes per token for a long run reaches bytesPerToken or more
// (tiktoken 0.13.0, runs from 6e3 to 2.4e4 runes, where the ratio saturates).
//
// The statistic is the LARGEST bytes per token of o200k_base, cl100k_base and
// o200k_harmony, which is the same as the SMALLEST token count. That choice is
// the whole correctness argument: the smallest count is the number the charge is
// compared against, so a member cannot over-charge on any of the three. Using
// the smallest bytes per token instead only collapses a run when every encoding
// compresses it, which is what first left nine over-charging runes out of this
// switch, U+2500 among them.
//
// Measured members, with the ratio that earns each one:
//
//	' ' 128 | '-' '=' '_' '/' '.' '*' '#' '%' 64
//	U+2014 U+2026 U+2500 U+25A1 U+3000 48 | U+2501 U+2550 24
//	'\n' '+' '~' 32 | '\t' ';' '!' ':' 'X' U+00A0 16
//	U+2013 U+200B U+2588 U+2605 U+2640 U+30FB U+30FC U+FF01 U+FF0A U+FF1D 12
//
// Everything else is counted at full byte length, because it already costs a
// token per 12 bytes or fewer and the plain estimate therefore under-counts it.
// The closest exclusions measure 8: '<' '>' '?' '@' '^' ',' U+00AF and the
// letters a f l o x A F. Then '|' '(' ')' '$' '\\' at 4, '{' '}' '[' ']' '&' and
// both quotes at 2, U+2000 U+205F U+2029 at 1.5, U+2028 U+202F at 3, and every
// ASCII control byte plus U+0085 next line and U+1680 ogham space mark at one
// real token per byte.
//
// That last group is why this is not the simpler test it looks like it should
// be. Collapsing whitespace by the Unicode White_Space property gives away real
// inference: U+1680 and U+0085 cost a provider token per byte while collapsing
// to nothing, and restricting the collapse to ASCII whitespace does not fix it,
// because U+000B vertical tab and U+000C form feed are ASCII whitespace and also
// cost a token per byte. Testing for non-alphanumeric instead over-collapses in
// the same way, and would also miss 'X', which is a member because a run of it
// measures 16. Membership has to follow the measured ratio and nothing else: the
// two directions are a vice, and a rule that is not measured escapes it on one
// side or the other.
//
// Cost of the conservative statistic, stated rather than hidden: for a rune
// whose ratio splits across encodings, the collapse gives up more against the
// encoding that splits the run. U+25A1 is the widest, 48 bytes per token on
// o200k_base and o200k_harmony but 1.5 on cl100k_base, so a pure run of it pays
// about 1 percent of what a cl100k-family model would count. That is the
// customer-favouring direction, it is bounded by the route's context window, and
// it buys the removal of a 2.5x over-charge on output models emit routinely.
//
// Families swept to build this list, each rune measured, not assumed: ASCII
// printable including letters and digits, Latin-1 punctuation and symbols,
// general punctuation U+2000 to U+206F, arrows, math operators, box drawing,
// block elements, geometric shapes, miscellaneous symbols, dingbats, CJK symbols
// and punctuation, katakana, halfwidth and fullwidth forms, the invisible format
// characters, and a sample of the emoji a model repeats to draw a bar. The
// measured rows live in token_estimate_scripts_test.go, which also fails if this
// switch ever collapses a rune with no row.
//
// ponytail: a fixed measured set, re-measure it (not just the divisor) if we
// onboard a tokenizer outside the o200k and cl100k families. Deriving the set at
// runtime would mean shipping a tokenizer into the settlement path, which is
// several orders more machinery than a fallback estimate deserves.
func runCollapsible(r rune) bool {
	switch r {
	case ' ', '\t', '\n',
		'-', '=', '_', '/', '.', '*', '#', '%', '+', '~', ';',
		'!', ':', 'X',
		'\u00a0', '\u200b', '\u3000', // NBSP, zero width space, ideographic space
		'\u2013', '\u2014', '\u2026', // en dash, em dash, horizontal ellipsis
		'\u2500', '\u2501', '\u2550', // box drawings light, heavy, double horizontal
		'\u2588', '\u25a1', '\u2605', '\u2640', // full block, white square, black star, female sign
		'\u30fb', '\u30fc', // katakana middle dot, prolonged sound mark
		'\uff01', '\uff0a', '\uff1d': // fullwidth exclamation, asterisk, equals
		return true
	}
	return false
}

// collapsedByteLen is the UTF-8 byte length of text, except that every run of a
// repeated runCollapsible rune contributes one byte per bytesPerToken bytes of
// the run instead of its full length. It allocates nothing, because the text it
// is handed can be megabytes of prompt.
//
// One byte per bytesPerToken bytes is the calibration, not a round number: it
// makes a pure run cost bytesPerToken squared, 144 bytes per estimated token,
// which clears the sparsest measured run (128 real bytes per token, for spaces)
// with about 12 percent of headroom. Any divisor under 11 would over-charge a
// space-padded prompt; collapsing a run to a single byte the way this used to
// makes the charge independent of the run's length, which is the shape that
// hands out unbilled inference.
//
// A run is one repeated rune, not any stretch of collapsible ones. A mixed
// stretch can be an adversarial alternation the vocabulary does not compress at
// all ("~;" repeated costs one real token per byte), so collapsing mixed
// stretches would reopen the give-up this closes.
func collapsedByteLen(text string) int {
	n := 0
	for i := 0; i < len(text); {
		// DecodeRuneInString rather than range plus utf8.RuneLen: RuneLen
		// reports 3 for the replacement rune a malformed byte decodes to, which
		// would count invalid input as larger than it is.
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		if !runCollapsible(r) {
			n += size
			continue
		}
		runBytes := size
		for i < len(text) {
			next, nextSize := utf8.DecodeRuneInString(text[i:])
			if next != r {
				break
			}
			runBytes += nextSize
			i += nextSize
		}
		n += (runBytes + bytesPerToken - 1) / bytesPerToken
	}
	return n
}

// clampZeroCompletionUsage rewrites usage.CompletionTokens when the upstream
// provider returned 0 but the response actually carried output text. A warning
// is logged so the billing team can track upstream flake rate. It then hands
// the object to EnforceUsageIdentity, which is what recomputes total_tokens --
// on every shape, not only this one.
//
// outputTexts must contain every choice's text content (chat: message.content;
// legacy completions: choice.text). Empty entries are ignored — they represent
// legitimate empty completions where ct=0 is correct.
//
// upstreamID + aliasID + endpoint are passed through purely for log context.
func clampZeroCompletionUsage(usage *UsageResponse, outputTexts []string, upstreamID, aliasID, endpoint string) {
	if usage == nil {
		return
	}
	if usage.CompletionTokens == 0 {
		var total int64
		for _, t := range outputTexts {
			total += estimateCompletionTokens(t)
		}
		// total == 0 with completion_tokens == 0 is the tool-call-only shape:
		// chatChoiceTexts reads message.content and message.refusal and
		// deliberately skips tool_calls, so a turn that emitted nothing but
		// tool-call arguments legitimately has no text to estimate from.
		// Leave the component alone -- an estimate of zero is not evidence of
		// zero output, and D-055 names this exact shape.
		//
		// The identity below still runs on it, and the intended reading of
		// that is deliberate rather than incidental: an upstream reporting
		// prompt 200, completion 0, total 260 on such a turn is describing 60
		// tokens of tool-call arguments it counted and did not attribute to
		// either component. The response is corrected to 200, so the 60
		// leaves the customer-facing payload as well as the ledger. That is
		// the same call the identity makes everywhere else -- the payload
		// states what Hive billed, and the unattributed quantity is carried
		// operator-side (the log line and the unaccounted-token counter in
		// EnforceUsageIdentity) rather than in a total no client can
		// reconcile. It is NOT a verified zero of tool-call output, the trap
		// D-056 documents for cache_write_tokens; it is an unmeasured
		// quantity, and the counter is where it is measured.
		// TestUsageIdentity_ToolCallOnlyTurnCorrectsTheTotalAndRecordsTheGap
		// pins that reading.
		if total > 0 {
			log.Printf("inference: usage clamp engaged endpoint=%s alias=%s upstream_id=%s upstream_ct=0 estimated_ct=%d",
				endpoint, aliasID, upstreamID, total)
			usage.CompletionTokens = total
		}
	}
	EnforceUsageIdentity(usage, upstreamID, aliasID, endpoint)
}

// Direction label values for the total_tokens counters, and convention label
// values for the reasoning counter. Named rather than spelled inline because
// NewStageMetrics pre-creates every series at boot and a typo in either place
// would create an extra, permanently-zero one.
const (
	usageIdentityOver  = "over"
	usageIdentityUnder = "under"

	// reasoningAlongside: the upstream's own arithmetic is
	// prompt + completion + reasoning == total, so it counts reasoning
	// OUTSIDE completion_tokens. Google's generateContent reports
	// totalTokenCount inclusive of thoughtsTokenCount while
	// candidatesTokenCount excludes it; that is the shape behind issue #1472.
	reasoningAlongside = "alongside"
	// reasoningUnexplained: reasoning_tokens exceeds completion_tokens and
	// the alongside arithmetic does not hold either, so NEITHER convention
	// describes the object. Nothing is rewritten on this shape.
	reasoningUnexplained = "unexplained"
)

// usageConvention is which token-accounting convention an upstream usage
// object is written in, decided from the numbers on the wire and from nothing
// else.
//
// Never from the provider or the model family, and that is a repository
// lesson rather than a preference: OpenRouter reports OpenAI-inclusive usage
// even for Claude models, which is why NormalizeCacheUsage already keys on
// field presence for the same class of question. A model-family switch here
// would be wrong for the same requests that one was wrong for.
type usageConvention int

const (
	// conventionInside is OpenAI's: reasoning_tokens is a subset of
	// completion_tokens and total_tokens is their sum with prompt_tokens, so
	// the wire identity already holds.
	conventionInside usageConvention = iota
	// conventionAlongside is the thoughts convention: reasoning is counted in
	// the total but excluded from completion_tokens.
	conventionAlongside
	// conventionUnknown is neither arithmetic holding. The object is not
	// self-consistent under any convention this gateway knows.
	conventionUnknown
)

// classifyUsageConvention decides the convention from the wire shape.
//
// Order matters. When reasoning is 0 or absent the two arithmetics coincide,
// and inside is the right answer there: it is OpenAI's convention, it is what
// the overwhelming majority of traffic is written in, and calling that shape
// "alongside" would label every ordinary response as a thinking model.
func classifyUsageConvention(usage *UsageResponse, reasoning int64) usageConvention {
	sum := usage.PromptTokens + usage.CompletionTokens
	switch {
	case usage.TotalTokens == sum && reasoning <= usage.CompletionTokens:
		// The breakdown clause is load-bearing, not decoration. A total that
		// equals the component sum while reasoning_tokens exceeds
		// completion_tokens is NOT the inside convention: under it the
		// breakdown is a subset of the component. Classifying that shape as
		// inside is what let it return early and reach a customer unexamined.
		return conventionInside
	case reasoning > 0 && usage.TotalTokens == sum+reasoning:
		return conventionAlongside
	default:
		return conventionUnknown
	}
}

// EnforceUsageIdentity holds a usage object to the OpenAI wire contract's
// total_tokens identity, which defines total_tokens as prompt_tokens plus
// completion_tokens rather than as an independently reported figure, and which
// every OpenAI-compatible client assumes (issue #1472).
//
// Exported so the two relays that never build a typed usage object can reach
// the same rule instead of writing a second copy of it by hand:
// apps/edge-api/internal/rag's synchronous half calls this directly, and both
// SSE relays call EnforceUsageIdentityInFrame below, which wraps it.
//
// # It never rewrites reasoning_tokens, and that is the whole design
//
// Nothing in edge-api computes reasoning_tokens. Every adapter decodes it
// verbatim from the upstream, so the field carries whatever convention the
// upstream wrote it in, and a rule derived from this package's own doc comment
// about what the field "means" is a rule derived from an assumption.
//
// An earlier version of this function capped reasoning_tokens at
// completion_tokens whenever it corrected a total downward, on the reasoning
// that a breakdown may not exceed the component it breaks down. That was wrong
// on exactly the shape this issue is about: when an upstream counts reasoning
// ALONGSIDE completion rather than inside it, 26 is a real measurement and 1
// is not a smaller version of it, so the cap handed the customer a fabricated
// number and lost 25 tokens that survived only in a log line. A measured
// upstream quantity is never shrunk here to satisfy an invariant the upstream
// did not claim to be writing under.
//
// # What it does instead: classify, then act per convention
//
//   - conventionInside (prompt + completion == total). The identity already
//     holds. Nothing is rewritten and nothing is logged, because a line on
//     every well-formed request buries the ones that matter.
//   - conventionAlongside (prompt + completion + reasoning == total). The
//     upstream is self-consistent under its own convention. total_tokens is
//     restated as the OpenAI derived figure so a client can reconcile it, and
//     reasoning_tokens is left EXACTLY as measured, which is what makes the
//     restatement lossless: the remainder is still on the wire, so a caller
//     reads 5 and 26 and can recover the upstream's 31. The quantity is also
//     recorded operator-side, in tokens, by both counters below.
//   - conventionUnknown. Neither arithmetic holds, so no convention explains
//     the object. Two sub-cases, handled differently on purpose:
//     when total disagrees with the component sum, the total is restated and
//     the signed discrepancy is recorded as a quantity; when the total agrees
//     but reasoning_tokens exceeds completion_tokens, NOTHING is rewritten,
//     because there is no second field carrying the remainder and any
//     rewrite would be this gateway inventing a figure. It is recorded and
//     passed through.
//
// # Why the correction never goes to the components
//
// Folding an unaccounted remainder into completion_tokens is what issue #1472
// suggests, and it is the one option that is not available without an owner
// ruling: completion_tokens is an input to the charge, so folding reasoning in
// begins billing a quantity that has never been billed on these paths, which
// D-055 forbids. Splitting the two (folding for the customer, billing the old
// figure) is refused for the reason clampUsageToCeiling states in
// completion_ceiling.go: one number in one place, so the caller can never read
// a completion count they were not billed on. Whether Hive should bill thought
// tokens at all is a pricing decision for the owner, and
// hive_usage_reasoning_tokens_unbilled_total is what puts the quantity in
// front of that decision.
//
// # Which paths price which number
//
// Stated concretely because an earlier version of this comment said "nothing
// anywhere prices total_tokens", that was false, and a false statement here is
// worse than no statement: this is the file the next person reads when they
// ask which number the money comes from. By symbol, not by line, because
// line-pinned citations into files another pull request is editing are stale
// on merge.
//
//   - Per-token aliases price the COMPONENTS. settlementCredits (stream.go)
//     passes prompt and completion, split into their cache classes, into
//     CreditsForTokens, and ChatSettlementCredits (pricing.go) funnels the
//     session-chat surface into the same function. CreditsForTokens takes four
//     token arguments and neither total_tokens nor reasoning_tokens is among
//     them.
//   - Variable-price (upstream_actual) aliases price the COST the upstream
//     reported for that generation, read from the raw response bytes by
//     UpstreamActualSettlement (pricing.go). No token count of ours enters
//     that charge at all.
//   - The BATCH path prices total_tokens. DefaultCreditPolicy.Credits
//     (apps/control-plane/internal/batchstore/executor/dispatcher.go) returns
//     usage.TotalTokens whenever it is above zero and falls back to the
//     component sum only otherwise, and it is live: the server main in
//     apps/control-plane/cmd/server/main.go constructs the dispatcher with a
//     nil policy, which NewDispatcher substitutes this default for. So on a
//     /v1/batches line the 31-against-5 shape behind this issue is a 6.2x
//     OVERCHARGE, not a reporting defect. That path decodes the raw LiteLLM
//     body in batchstore/local_inference.go and never crosses this function,
//     so nothing in this file reaches it. It is tracked in issue #1473 and
//     corrected there, in the same function as its sibling defect, not here.
//
// So on the paths this function does cover, restating the total moves no money
// in either direction. That is a statement about those paths, not a claim that
// the total_tokens class is safe everywhere.
//
// # The record left behind
//
// Loud, never silent, and as a quantity rather than only as prose. Every
// counter is incremented AFTER the object has actually been corrected rather
// than before, so none of them can report a state the usage object has not
// reached:
//
//   - usageIdentityViolations counts OCCURRENCES of a restated total, by
//     alias, endpoint and direction.
//   - usageIdentityUnaccountedTokens sums the TOKENS between the upstream
//     total and the component sum, by direction. It exists because an
//     occurrence count cannot answer "how many tokens went unaccounted
//     yesterday", and because restating the total removes the only place that
//     quantity used to be durable by accident: usage_events.hive_credit_delta
//     carried usage.TotalTokens (recordCompletedEvent in orchestrator.go,
//     recordInterruptedEvent in stream.go) and now carries the component sum.
//   - reasoningTokensUnbilled sums the REASONING TOKENS Hive reported and did
//     not bill, by convention. On the alongside shape it is the quantity an
//     owner needs to price the D-055 decision. On the unexplained shape it is
//     the only record that a breakdown larger than its own component was
//     served, since nothing is rewritten there and the total counters see a
//     discrepancy of zero.
//
// One shape deliberately increments on every response, and it is worth naming
// so nobody reads it as a defect later. An upstream that reports components
// and omits total_tokens decodes to a total of 0, which no convention explains,
// so it lands in the unknown branch as "under" on each response it serves.
// That is correct: an absent derived field IS a disagreement with the
// components, and filling it in is the one restatement that overrides no
// reported figure at all. It is not expected to be common. Every provider this
// gateway has routed to reports total_tokens, LiteLLM normalizes the usage
// block, and no shape in this repository's fixtures omits it outside the
// deliberate TotalAbsentIsFilledFromComponents case. If the rate does climb,
// the reading is not "usage identity violations are up" but "one provider's
// usage adapter stopped reporting a total", and usageIdentityViolations
// carries the alias label that names which one. A counter that fires on every
// response from some provider is noise, and noise gets ignored, which is the
// same thing as having no counter at all.
func EnforceUsageIdentity(usage *UsageResponse, upstreamID, aliasID, endpoint string) {
	if usage == nil {
		return
	}
	var reasoning int64
	if usage.CompletionTokensDetails != nil {
		reasoning = usage.CompletionTokensDetails.ReasoningTokens
	}
	convention := classifyUsageConvention(usage, reasoning)
	if convention == conventionInside {
		return
	}

	sum := usage.PromptTokens + usage.CompletionTokens
	// Signed: positive means the upstream's total exceeded its own components
	// (tokens it counted and did not attribute), negative means it fell short.
	// Zero on the shape where the total agrees and only the breakdown is
	// impossible, which is why that shape is recorded on the reasoning counter
	// instead.
	unaccounted := usage.TotalTokens - sum

	switch convention {
	case conventionAlongside:
		log.Printf("inference: usage identity violated endpoint=%s alias=%s upstream_id=%s prompt_tokens=%d completion_tokens=%d "+
			"upstream_total_tokens=%d corrected_total_tokens=%d unaccounted_tokens=%d reported_reasoning_tokens=%d convention=%s: "+
			"the upstream counts reasoning tokens alongside completion_tokens rather than inside it, so its total is self-consistent under its own "+
			"convention; restating the total as the component sum, leaving reasoning_tokens exactly as measured so the remainder stays on the wire, "+
			"and the charge is unchanged because it prices the components",
			endpoint, aliasID, upstreamID, usage.PromptTokens, usage.CompletionTokens,
			usage.TotalTokens, sum, unaccounted, reasoning, reasoningAlongside)
		usage.TotalTokens = sum
		recordTotalRestated(aliasID, endpoint, unaccounted)
		reasoningTokensUnbilled.WithLabelValues(reasoningAlongside).Add(float64(reasoning))

	case conventionUnknown:
		if unaccounted == 0 {
			// The total agrees with the components and reasoning_tokens still
			// exceeds completion_tokens, so the object is impossible under the
			// inside convention and not described by the alongside one either.
			// Nothing here is rewritten: there is no second field carrying the
			// remainder, so any figure this gateway wrote would be invented,
			// and the reported measurements are passed through with the fact
			// recorded instead.
			log.Printf("inference: usage identity violated endpoint=%s alias=%s upstream_id=%s prompt_tokens=%d completion_tokens=%d "+
				"total_tokens=%d reported_reasoning_tokens=%d convention=%s: "+
				"reasoning_tokens exceeds completion_tokens while the total agrees with the components, so neither the inside nor the alongside "+
				"convention describes this object; passing the upstream figures through untouched rather than inventing one",
				endpoint, aliasID, upstreamID, usage.PromptTokens, usage.CompletionTokens,
				usage.TotalTokens, reasoning, reasoningUnexplained)
			reasoningTokensUnbilled.WithLabelValues(reasoningUnexplained).Add(float64(reasoning))
			return
		}
		log.Printf("inference: usage identity violated endpoint=%s alias=%s upstream_id=%s prompt_tokens=%d completion_tokens=%d "+
			"upstream_total_tokens=%d corrected_total_tokens=%d unaccounted_tokens=%d reported_reasoning_tokens=%d convention=%s: "+
			"the upstream's total disagrees with its own components and no reasoning count explains the gap; reporting the component sum, "+
			"and the charge is unchanged because it prices the components",
			endpoint, aliasID, upstreamID, usage.PromptTokens, usage.CompletionTokens,
			usage.TotalTokens, sum, unaccounted, reasoning, "unknown")
		usage.TotalTokens = sum
		recordTotalRestated(aliasID, endpoint, unaccounted)

	case conventionInside:
		// Unreachable: the early return above takes every inside object, and
		// classifyUsageConvention only returns inside when the breakdown fits
		// inside its own component. Left explicit so adding a fourth
		// convention has to decide what happens here rather than falling
		// through in silence.
	}
}

// recordTotalRestated increments the two total_tokens counters, after the
// restatement rather than before, and converts the signed discrepancy into the
// magnitude the quantity series carries.
func recordTotalRestated(aliasID, endpoint string, unaccounted int64) {
	direction := usageIdentityOver
	magnitude := unaccounted
	if unaccounted < 0 {
		direction = usageIdentityUnder
		magnitude = -unaccounted
	}
	usageIdentityViolations.WithLabelValues(aliasID, endpoint, direction).Inc()
	usageIdentityUnaccountedTokens.WithLabelValues(direction).Add(float64(magnitude))
}

// EnforceUsageIdentityInFrame holds the usage member of one already-sanitized
// response frame to the same rule EnforceUsageIdentity applies to a typed
// usage object, for the two relays that hand raw frame bytes to a customer and
// never build one: session chat (apps/edge-api/internal/chat/dispatch.go,
// which serves the Open WebUI front end) and RAG chat
// (apps/edge-api/internal/rag/chat_handler.go). Before this existed, a
// violating total reached both of those surfaces verbatim while the four
// API-key endpoints were corrected, which is a guarantee that held in four
// places out of six.
//
// total_tokens is the only key it ever writes, because it is the only field
// EnforceUsageIdentity ever rewrites. It patches that one key in place and
// leaves every other byte as the sanitizer produced it: re-marshalling a typed
// UsageResponse instead would silently drop any usage member this package does
// not declare, and packages/sanitize deliberately keeps usage as an open map
// minus three cost fields, so a round trip through the struct would be a
// second, invisible sanitiser.
//
// On any parse or encode failure the ORIGINAL bytes are returned. A failed
// cosmetic rewrite must never break a frame already committed to the wire; the
// charge on both callers prices components regardless of what these bytes say.
// A frame carrying no usage member is returned untouched and logs nothing,
// which is almost every frame of a stream.
func EnforceUsageIdentityInFrame(frame []byte, upstreamID, aliasID, endpoint string) []byte {
	if len(frame) == 0 {
		return frame
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(frame, &fields); err != nil || fields == nil {
		return frame
	}
	rawUsage, present := fields["usage"]
	if !present {
		return frame
	}
	var usageFields map[string]json.RawMessage
	if err := json.Unmarshal(rawUsage, &usageFields); err != nil || usageFields == nil {
		return frame
	}
	var usage UsageResponse
	if err := json.Unmarshal(rawUsage, &usage); err != nil {
		return frame
	}

	totalBefore := usage.TotalTokens
	EnforceUsageIdentity(&usage, upstreamID, aliasID, endpoint)
	if usage.TotalTokens == totalBefore {
		return frame
	}

	total, err := json.Marshal(usage.TotalTokens)
	if err != nil {
		log.Printf("inference: could not encode a corrected total into a relayed frame endpoint=%s alias=%s: %v", endpoint, aliasID, err)
		return frame
	}
	usageFields["total_tokens"] = total

	rebuiltUsage, err := json.Marshal(usageFields)
	if err != nil {
		log.Printf("inference: could not re-encode a corrected usage member endpoint=%s alias=%s: %v", endpoint, aliasID, err)
		return frame
	}
	fields["usage"] = rebuiltUsage
	out, err := json.Marshal(fields)
	if err != nil {
		log.Printf("inference: could not re-encode a relayed frame after a usage correction endpoint=%s alias=%s: %v", endpoint, aliasID, err)
		return frame
	}
	return out
}

// chatChoiceTexts returns the text content of every chat completion choice.
// nil-safe and refusal-aware: refusal strings are also counted because they
// represent generated assistant output that consumed completion tokens.
func chatChoiceTexts(choices []ChatCompletionChoice) []string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		if c.Message.Content != nil && *c.Message.Content != "" {
			out = append(out, *c.Message.Content)
		}
		if c.Message.Refusal != nil && *c.Message.Refusal != "" {
			out = append(out, *c.Message.Refusal)
		}
	}
	return out
}

// completionChoiceTexts returns the text of every legacy completion choice.
func completionChoiceTexts(choices []CompletionChoice) []string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		if c.Text != "" {
			out = append(out, c.Text)
		}
	}
	return out
}

// responsesOutputTexts returns the visible output_text content of every
// Responses API message item. Tool-call and reasoning items have no billable
// completion-text contribution (reasoning tokens are tracked separately via
// usage.completion_tokens_details.reasoning_tokens) and are skipped.
func responsesOutputTexts(items []ResponseOutputItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" && part.Text != "" {
				out = append(out, part.Text)
			}
		}
	}
	return out
}

// responseText pulls the generated text back out of an already-normalized
// synchronous response, for the completion-side half of the settlement
// estimate when the provider returned no usage block at all (issue #636).
// It is the response-side mirror of promptText below.
//
// It re-parses the normalized bytes rather than having every normalizeFunc
// return its output text, because it is only ever called on the anomaly path:
// a response that did carry usage never reaches here, so the normal path pays
// nothing for this.
//
// Embeddings return the empty string: an embeddings response carries vectors,
// not generated text, so there is no completion quantity to estimate from.
// Settlement therefore releases the hold in full for an embeddings response
// with no usage block. That under-charges an anomaly whose hold is only 1000
// credits, which is the customer-favouring direction and the correct one for a
// quantity nobody measured.
// ponytail: no embeddings-side input estimate, add one if providers turn out
// to omit embeddings usage often enough to matter (it is logged when it
// happens).
func responseText(endpoint string, normalized []byte) string {
	var texts []string
	switch endpoint {
	case EndpointChatCompletions:
		var resp ChatCompletionResponse
		if err := json.Unmarshal(normalized, &resp); err != nil {
			return ""
		}
		texts = chatChoiceTexts(resp.Choices)
	case EndpointCompletions:
		var resp CompletionResponse
		if err := json.Unmarshal(normalized, &resp); err != nil {
			return ""
		}
		texts = completionChoiceTexts(resp.Choices)
	case EndpointResponses:
		var resp ResponseObject
		if err := json.Unmarshal(normalized, &resp); err != nil {
			return ""
		}
		texts = responsesOutputTexts(resp.Output)
	default:
		return ""
	}
	return strings.Join(texts, "")
}

// --- Prompt text extraction (issue #602) ---
//
// promptText pulls the human-authored text out of a raw OpenAI-compatible
// request body for the prompt-side half of the disconnect-settlement
// estimate. It deliberately does NOT read the raw request bytes: the raw
// body also carries field names, sampling params, tool schemas, and (for
// multimodal messages) base64 image data URIs, none of which are prompt
// tokens. Counting raw bytes let an ordinary image-attached chat request
// estimate millions of "tokens" from a few hundred KB of base64, which is
// the root cause of issue #602's over-charge.
//
// Images: excluded from this estimate entirely, not approximated by byte
// length or a fixed per-image allowance. This is a fallback path -- it only
// runs when the upstream never confirmed real usage -- so under-counting a
// multimodal prompt here is the safe direction, and the hard hold-clamp in
// control-plane's finalizeLocked is the backstop against any remaining
// overcount regardless of what this function returns.
func promptText(endpoint string, body []byte) string {
	switch endpoint {
	case EndpointChatCompletions:
		return chatRequestText(body)
	case EndpointCompletions:
		return completionRequestText(body)
	case EndpointResponses:
		return responsesRequestText(body)
	default:
		return ""
	}
}

// textCarrier matches any request-side object shaped { "content": ... },
// covering chat messages and Responses API input items alike.
type textCarrier struct {
	Content json.RawMessage `json:"content"`
}

// textPart matches one multimodal content-array entry. Only "text" /
// "input_text" parts contribute; image/audio parts are skipped by omission.
type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// contentText resolves a message "content" field that is either a plain
// string or an array of typed parts (the OpenAI multimodal shape).
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []textPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "input_text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// chatRequestText extracts every message's text content from a raw
// /v1/chat/completions request body.
func chatRequestText(body []byte) string {
	var req struct {
		Messages []textCarrier `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, m := range req.Messages {
		sb.WriteString(contentText(m.Content))
	}
	return sb.String()
}

// completionRequestText extracts the prompt from a raw legacy
// /v1/completions request body. prompt is either a string or an array of
// strings per the OpenAI spec.
func completionRequestText(body []byte) string {
	var req struct {
		Prompt json.RawMessage `json:"prompt"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(req.Prompt, &s); err == nil {
		return s
	}
	var arr []string
	if err := json.Unmarshal(req.Prompt, &arr); err == nil {
		return strings.Join(arr, "")
	}
	return ""
}

// responsesRequestText extracts instructions + input text from a raw
// /v1/responses request body. input is either a string or an array of
// message-shaped items.
func responsesRequestText(body []byte) string {
	var req struct {
		Input        json.RawMessage `json:"input"`
		Instructions *string         `json:"instructions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	var sb strings.Builder
	if req.Instructions != nil {
		sb.WriteString(*req.Instructions)
	}
	var s string
	if err := json.Unmarshal(req.Input, &s); err == nil {
		sb.WriteString(s)
		return sb.String()
	}
	var items []textCarrier
	if err := json.Unmarshal(req.Input, &items); err == nil {
		for _, it := range items {
			sb.WriteString(contentText(it.Content))
		}
	}
	return sb.String()
}
