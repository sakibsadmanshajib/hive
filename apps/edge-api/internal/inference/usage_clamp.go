package inference

import (
	"encoding/json"
	"log"
	"strings"
)

// estimateCompletionTokens returns a conservative cl100k-style approximation
// of the token count for a piece of text. The exact formula does not matter —
// it only kicks in when the upstream provider returns completion_tokens=0 on
// a non-empty assistant message, which is a billing-leak case (see git
// history for the flaky-usage-tokens root-cause debug notes).
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
