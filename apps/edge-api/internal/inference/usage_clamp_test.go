package inference

import (
	"strings"
	"testing"
)

func ptrStr(s string) *string { return &s }

func TestEstimateCompletionTokens(t *testing.T) {
	// These pin the floor and the shape of the formula only. Everything that
	// matters about the arithmetic is asserted in token_estimate_scripts_test.go
	// at thousands of tokens, because the 1-credit floor below swallows any
	// mispricing at this size (issue #673).
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"empty", "", 0},
		{"single char", "x", 1},
		{"three chars", "abc", 1},
		{"twelve bytes is one token", "abcdefghijkl", 1},
		{"thirteen bytes rounds up", "abcdefghijklm", 2},
		{"hello world", "hello world", 1},
		{"long sentence", "The quick brown fox jumps over the lazy dog", 4},
		{"whitespace only floors at one, never zero", "          ", 1},
		{"a whitespace run costs one byte per twelve", "a" + strings.Repeat(" ", 100) + "b", 1},
		{"a separator run costs one byte per twelve", "a" + strings.Repeat("-", 100) + "b", 1},
		{"a control byte run is counted in full", strings.Repeat("\v", 100), 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateCompletionTokens(tt.in); got != tt.want {
				t.Fatalf("estimateCompletionTokens(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestClampZeroCompletionUsage_NilUsage(t *testing.T) {
	clampZeroCompletionUsage(nil, []string{"hello"}, "id", "alias", EndpointChatCompletions)
}

func TestClampZeroCompletionUsage_NoClampWhenNonZero(t *testing.T) {
	u := &UsageResponse{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	clampZeroCompletionUsage(u, []string{"hello"}, "id", "alias", EndpointChatCompletions)
	if u.CompletionTokens != 5 || u.TotalTokens != 15 {
		t.Fatalf("unexpected mutation: %+v", u)
	}
}

func TestClampZeroCompletionUsage_NoClampWhenAllOutputsEmpty(t *testing.T) {
	u := &UsageResponse{PromptTokens: 10, CompletionTokens: 0, TotalTokens: 10}
	clampZeroCompletionUsage(u, []string{"", ""}, "id", "alias", EndpointChatCompletions)
	if u.CompletionTokens != 0 || u.TotalTokens != 10 {
		t.Fatalf("unexpected mutation: %+v", u)
	}
}

func TestClampZeroCompletionUsage_ClampsWhenZeroOnNonEmptyOutput(t *testing.T) {
	u := &UsageResponse{PromptTokens: 4, CompletionTokens: 0, TotalTokens: 4}
	clampZeroCompletionUsage(u, []string{"ok\n"}, "gen-1777055988", "hive-default", EndpointChatCompletions)
	if u.CompletionTokens == 0 {
		t.Fatalf("expected ct to be clamped > 0, got 0")
	}
	if u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		t.Fatalf("total_tokens not recomputed: pt=%d ct=%d tt=%d",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
}

func TestClampZeroCompletionUsage_ChatRefusalCounted(t *testing.T) {
	choices := []ChatCompletionChoice{
		{Message: ChatCompletionMessage{Role: "assistant", Content: nil, Refusal: ptrStr("I cannot help with that")}},
	}
	u := &UsageResponse{PromptTokens: 5, CompletionTokens: 0, TotalTokens: 5}
	clampZeroCompletionUsage(u, chatChoiceTexts(choices), "id", "alias", EndpointChatCompletions)
	if u.CompletionTokens == 0 {
		t.Fatalf("expected refusal to clamp ct, got 0")
	}
}

func TestClampZeroCompletionUsage_ReasoningTokensPreserved(t *testing.T) {
	u := &UsageResponse{
		PromptTokens:     4,
		CompletionTokens: 0,
		TotalTokens:      4,
		CompletionTokensDetails: &CompletionTokensDetails{
			ReasoningTokens: 42,
		},
	}
	clampZeroCompletionUsage(u, []string{"ok"}, "id", "alias", EndpointChatCompletions)
	if u.CompletionTokensDetails == nil || u.CompletionTokensDetails.ReasoningTokens != 42 {
		t.Fatalf("reasoning_tokens mutated: %+v", u.CompletionTokensDetails)
	}
	if u.CompletionTokens == 0 {
		t.Fatalf("expected ct to be clamped, got 0")
	}
}

func TestNormalizeChatCompletion_ClampsZeroCt(t *testing.T) {
	// Real-world body captured during 2026-04-24 staging burst (sample 15).
	body := []byte(`{"id":"gen-1777055988-QQJOozdBjyfzu9oHoERL","object":"chat.completion","created":1777055988,"model":"hive-default","choices":[{"index":0,"message":{"role":"assistant","content":"ok\n"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}`)
	_, usage, err := normalizeChatCompletion(body, "hive-default")
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil {
		t.Fatal("usage nil")
	}
	if usage.CompletionTokens == 0 {
		t.Fatalf("clamp did not engage: %+v", usage)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total_tokens not recomputed: %+v", usage)
	}
}

func TestNormalizeCompletion_ClampsZeroCt(t *testing.T) {
	body := []byte(`{"id":"cmpl-zct","object":"text_completion","created":0,"model":"r","choices":[{"text":"ok","index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}}`)
	_, usage, err := normalizeCompletion(body, "alias")
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.CompletionTokens == 0 {
		t.Fatalf("clamp did not engage: %+v", usage)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total_tokens not recomputed: %+v", usage)
	}
}

func TestNormalizeChatCompletion_PreservesNonZeroCt(t *testing.T) {
	body := []byte(`{"id":"x","object":"chat.completion","created":0,"model":"r","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`)
	_, usage, err := normalizeChatCompletion(body, "alias")
	if err != nil {
		t.Fatal(err)
	}
	if usage.CompletionTokens != 7 || usage.TotalTokens != 12 {
		t.Fatalf("clamp wrongly mutated nonzero usage: %+v", usage)
	}
}

func TestNormalizeResponsesSync_ClampsZeroCt(t *testing.T) {
	// Upstream chat body with nonempty content and completion_tokens=0.
	body := []byte(`{"id":"gen-zct-responses","object":"chat.completion","created":0,"model":"hive-default","choices":[{"index":0,"message":{"role":"assistant","content":"ok\n"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}`)
	req := ResponsesRequest{Model: "hive-default"}
	_, usage, err := normalizeResponsesSync(body, "hive-default", req)
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil {
		t.Fatal("usage nil")
	}
	if usage.CompletionTokens == 0 {
		t.Fatalf("clamp did not engage: %+v", usage)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total_tokens not recomputed: %+v", usage)
	}
}

func TestNormalizeResponsesSync_PreservesNonZeroCt(t *testing.T) {
	body := []byte(`{"id":"r","object":"chat.completion","created":0,"model":"hive-default","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`)
	req := ResponsesRequest{Model: "hive-default"}
	_, usage, err := normalizeResponsesSync(body, "hive-default", req)
	if err != nil {
		t.Fatal(err)
	}
	if usage.CompletionTokens != 7 || usage.TotalTokens != 12 {
		t.Fatalf("clamp wrongly mutated nonzero usage: %+v", usage)
	}
}

func TestResponsesOutputTexts_IgnoresNonOutputText(t *testing.T) {
	items := []ResponseOutputItem{
		{
			Type: "message",
			Content: []ResponseContentPart{
				{Type: "output_text", Text: "hello"},
				{Type: "output_text", Text: ""},      // empty — skipped
				{Type: "reasoning", Text: "hidden"},  // non-output_text part — skipped
			},
		},
		{
			Type:    "reasoning", // reasoning item — whole item skipped (reasoning_tokens tracked separately)
			Content: []ResponseContentPart{{Type: "output_text", Text: "deep thoughts"}},
		},
	}
	got := responsesOutputTexts(items)
	if len(got) != 1 {
		t.Fatalf("unexpected output texts: %+v", got)
	}
	if got[0] != "hello" {
		t.Fatalf("first text wrong: %q", got[0])
	}
}

func TestUsageAccumulator_AccumulateContentAndClamp(t *testing.T) {
	acc := &UsageAccumulator{}
	// Deltas.
	acc.AccumulateContent(ChatCompletionChunk{
		Choices: []ChunkChoice{{Delta: ChunkDelta{Content: ptrStr("hello ")}}},
	})
	acc.AccumulateContent(ChatCompletionChunk{
		Choices: []ChunkChoice{{Delta: ChunkDelta{Content: ptrStr("world")}}},
	})
	// Terminal usage chunk with ct=0 — should be clamped.
	usage := &UsageResponse{PromptTokens: 3, CompletionTokens: 0, TotalTokens: 3}
	acc.ClampUsage(usage, "chatcmpl-stream", "hive-default", EndpointChatCompletions)
	if usage.CompletionTokens == 0 {
		t.Fatalf("expected clamp to engage on streamed content, got 0")
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total_tokens not recomputed: %+v", usage)
	}
}

func TestUsageAccumulator_ClampNoopWithEmptyContent(t *testing.T) {
	acc := &UsageAccumulator{}
	// No deltas accumulated — legit empty stream (e.g. tool-call only).
	usage := &UsageResponse{PromptTokens: 5, CompletionTokens: 0, TotalTokens: 5}
	acc.ClampUsage(usage, "id", "alias", EndpointChatCompletions)
	if usage.CompletionTokens != 0 || usage.TotalTokens != 5 {
		t.Fatalf("clamp engaged on empty content: %+v", usage)
	}
}

func TestUsageAccumulator_ClampPreservesNonZero(t *testing.T) {
	acc := &UsageAccumulator{}
	acc.AccumulateContent(ChatCompletionChunk{
		Choices: []ChunkChoice{{Delta: ChunkDelta{Content: ptrStr("hi")}}},
	})
	usage := &UsageResponse{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}
	acc.ClampUsage(usage, "id", "alias", EndpointChatCompletions)
	if usage.CompletionTokens != 4 || usage.TotalTokens != 7 {
		t.Fatalf("clamp wrongly mutated nonzero usage: %+v", usage)
	}
}

// TestPromptText_ChatCompletionsExcludesImageData is the regression guard
// for issue #602's over-charge root cause: a multimodal chat request's
// base64 image data URI must never be counted as prompt text. Counting raw
// request bytes let an ordinary image-attached request estimate millions of
// "tokens" from a few hundred KB of base64 (base64 tokenizes far below
// byte_len/4), producing a settlement charge many multiples of the
// reservation hold.
func TestPromptText_ChatCompletionsExcludesImageData(t *testing.T) {
	hugeBase64 := strings.Repeat("A", 200_000) // stand-in for a real image payload
	body := []byte(`{"model":"hive-default","messages":[` +
		`{"role":"user","content":[` +
		`{"type":"text","text":"describe this image"},` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + hugeBase64 + `"}}` +
		`]}]}`)

	got := promptText(EndpointChatCompletions, body)
	if got != "describe this image" {
		t.Fatalf("expected only the text part, got %d bytes: %q...", len(got), got[:min(40, len(got))])
	}
	if estimateCompletionTokens(got) > 100 {
		t.Fatalf("estimate exploded despite excluding image data: %d tokens from %q", estimateCompletionTokens(got), got)
	}
}

func TestPromptText_ChatCompletionsStringContent(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"system","content":"be helpful"},{"role":"user","content":"hi there"}]}`)
	got := promptText(EndpointChatCompletions, body)
	if got != "be helpfulhi there" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptText_ChatCompletionsMalformedBodyReturnsEmpty(t *testing.T) {
	if got := promptText(EndpointChatCompletions, []byte(`not json`)); got != "" {
		t.Fatalf("expected empty text for malformed body, got %q", got)
	}
}

func TestPromptText_CompletionsStringAndArrayPrompt(t *testing.T) {
	if got := promptText(EndpointCompletions, []byte(`{"model":"m","prompt":"once upon a time"}`)); got != "once upon a time" {
		t.Fatalf("string prompt: got %q", got)
	}
	if got := promptText(EndpointCompletions, []byte(`{"model":"m","prompt":["a","b","c"]}`)); got != "abc" {
		t.Fatalf("array prompt: got %q", got)
	}
}

func TestPromptText_ResponsesStringInputAndInstructions(t *testing.T) {
	body := []byte(`{"model":"m","instructions":"be terse","input":"summarize this"}`)
	got := promptText(EndpointResponses, body)
	if got != "be tersesummarize this" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptText_ResponsesArrayInputExcludesImageParts(t *testing.T) {
	hugeBase64 := strings.Repeat("B", 200_000)
	body := []byte(`{"model":"m","input":[{"role":"user","content":[` +
		`{"type":"input_text","text":"what is this"},` +
		`{"type":"input_image","image_url":"data:image/png;base64,` + hugeBase64 + `"}` +
		`]}]}`)
	got := promptText(EndpointResponses, body)
	if got != "what is this" {
		t.Fatalf("expected only input_text, got %d bytes", len(got))
	}
}

func TestPromptText_UnknownEndpointReturnsEmpty(t *testing.T) {
	if got := promptText("embeddings", []byte(`{"input":"x"}`)); got != "" {
		t.Fatalf("expected empty text for unhandled endpoint, got %q", got)
	}
}

func TestUsageAccumulator_AccumulateContentRefusal(t *testing.T) {
	acc := &UsageAccumulator{}
	acc.AccumulateContent(ChatCompletionChunk{
		Choices: []ChunkChoice{{Delta: ChunkDelta{Refusal: ptrStr("I cannot help with that")}}},
	})
	usage := &UsageResponse{PromptTokens: 5, CompletionTokens: 0, TotalTokens: 5}
	acc.ClampUsage(usage, "id", "alias", EndpointChatCompletions)
	if usage.CompletionTokens == 0 {
		t.Fatalf("refusal content should clamp ct, got 0")
	}
}
