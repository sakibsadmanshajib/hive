package inference

import "log"

// estimateCompletionTokens returns a conservative cl100k-style approximation
// of the token count for a piece of text. The exact formula does not matter —
// it only kicks in when the upstream provider returns completion_tokens=0 on
// a non-empty assistant message, which is a billing-leak case (see
// .planning/debug/flaky-usage-tokens-root-cause.md).
//
// Heuristic: ceil(byte_len / 4), with a minimum of 1 when there is any text.
func estimateCompletionTokens(text string) int64 {
	if text == "" {
		return 0
	}
	n := int64((len(text) + 3) / 4)
	if n < 1 {
		return 1
	}
	return n
}

// clampZeroCompletionUsage rewrites usage.CompletionTokens when the upstream
// provider returned 0 but the response actually carried output text. It then
// recomputes total_tokens. A warning is logged so the billing team can track
// upstream flake rate.
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
	if usage.CompletionTokens > 0 {
		return
	}

	var total int64
	for _, t := range outputTexts {
		total += estimateCompletionTokens(t)
	}
	if total == 0 {
		// Legit empty completion (e.g. tool-call only). Leave usage alone.
		return
	}

	log.Printf("inference: usage clamp engaged endpoint=%s alias=%s upstream_id=%s upstream_ct=0 estimated_ct=%d",
		endpoint, aliasID, upstreamID, total)
	usage.CompletionTokens = total
	usage.TotalTokens = usage.PromptTokens + total
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
