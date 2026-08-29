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
// the object to enforceUsageIdentity, which is what recomputes total_tokens --
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
		// total == 0 is a legit empty completion (e.g. tool-call only): leave
		// the component alone. The identity below still applies to it.
		if total > 0 {
			log.Printf("inference: usage clamp engaged endpoint=%s alias=%s upstream_id=%s upstream_ct=0 estimated_ct=%d",
				endpoint, aliasID, upstreamID, total)
			usage.CompletionTokens = total
		}
	}
	enforceUsageIdentity(usage, upstreamID, aliasID, endpoint)
}

// enforceUsageIdentity makes total_tokens equal prompt_tokens plus
// completion_tokens, which the OpenAI wire contract defines as an identity
// rather than an independently reported figure, and which every
// OpenAI-compatible client assumes (issue #1472).
//
// Why the correction goes to the TOTAL and never to the components. The charge
// is priced from the components: settlementCredits and ChatSettlementCredits
// pass prompt and completion (split into their cache classes) into
// CreditsForTokens, and nothing anywhere prices total_tokens. So rewriting the
// total moves no money in either direction, while folding an unaccounted
// remainder into completion_tokens would start billing a class that has never
// been billed, which D-055 forbids without an owner ruling. When the gap is
// reasoning tokens a thinking model spent (the shape behind #1472: Google
// reports totalTokenCount inclusive of thoughtsTokenCount while
// candidatesTokenCount excludes it), whether Hive should bill them is a
// pricing decision for the owner, and the log line below is what puts the
// quantity in front of that decision instead of burying it.
//
// The decision is keyed on the numbers alone, never on a provider or model
// family: OpenRouter reports OpenAI-inclusive usage even for Claude models, so
// wire shape is the only sound discriminator here (see NormalizeCacheUsage,
// which keys on field presence for the same reason).
//
// A discrepancy is corrected loudly, never silently. A silent correction would
// hide a provider changing its accounting under us, which is exactly how this
// defect reached production unnoticed.
func enforceUsageIdentity(usage *UsageResponse, upstreamID, aliasID, endpoint string) {
	if usage == nil {
		return
	}
	sum := usage.PromptTokens + usage.CompletionTokens
	if usage.TotalTokens == sum {
		return
	}

	// Signed: positive means the upstream's total exceeded its own components
	// (tokens it counted and did not attribute), negative means it fell short.
	unaccounted := usage.TotalTokens - sum
	direction := "over"
	if unaccounted < 0 {
		direction = "under"
	}
	var reportedReasoning int64
	if usage.CompletionTokensDetails != nil {
		reportedReasoning = usage.CompletionTokensDetails.ReasoningTokens
	}
	log.Printf("inference: usage identity violated endpoint=%s alias=%s upstream_id=%s prompt_tokens=%d completion_tokens=%d "+
		"upstream_total_tokens=%d corrected_total_tokens=%d unaccounted_tokens=%d reported_reasoning_tokens=%d: "+
		"the upstream's total disagrees with its own components; reporting the component sum, and the charge is unchanged because it prices the components",
		endpoint, aliasID, upstreamID, usage.PromptTokens, usage.CompletionTokens,
		usage.TotalTokens, sum, unaccounted, reportedReasoning)
	usageIdentityViolations.WithLabelValues(aliasID, endpoint, direction).Inc()

	usage.TotalTokens = sum
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
