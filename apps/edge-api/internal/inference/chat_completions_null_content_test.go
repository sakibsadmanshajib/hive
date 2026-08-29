package inference

import (
	"encoding/json"
	"testing"
)

// TestNormalizeChatCompletion_NullContentCoercedToEmptyString reproduces the
// live regression from deploy run 32879931588 (packages/sdk-tests/js
// tests/chat-completions/chat-completions.test.ts:36): a hive-free pool
// member (PR #1115/#1155) returned a tool-free "Say hello" completion with
// message.content omitted entirely (null after unmarshal) after burning its
// token budget on hidden reasoning. Every OpenAI SDK types content as a plain
// string and dereferences it unconditionally, so a bare `null` broke the
// client. The normalization boundary must always hand back a string here.
func TestNormalizeChatCompletion_NullContentCoercedToEmptyString(t *testing.T) {
	body := []byte(`{"id":"gen-nullcontent","object":"chat.completion","created":0,"model":"route-free-pool-groq","choices":[{"index":0,"message":{"role":"assistant"},"finish_reason":"length"}],"usage":{"prompt_tokens":5,"completion_tokens":256,"total_tokens":261}}`)

	normalized, _, err := normalizeChatCompletion(body, "hive-free")
	if err != nil {
		t.Fatal(err)
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(normalized, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg.Content == nil {
		t.Fatal("content is nil, want a non-nil pointer to an empty string")
	}
	if *msg.Content != "" {
		t.Fatalf("content = %q, want empty string", *msg.Content)
	}

	// The raw bytes sent to the client must contain a JSON string, never the
	// literal null: an SDK checks the wire shape, not just Go's unmarshal.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &raw); err != nil {
		t.Fatal(err)
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(raw["choices"], &choices); err != nil {
		t.Fatal(err)
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(choices[0]["message"], &message); err != nil {
		t.Fatal(err)
	}
	if string(message["content"]) != `""` {
		t.Fatalf("wire content = %s, want an empty JSON string", message["content"])
	}
}

// TestNormalizeChatCompletion_NullContentPreservedWithToolCalls asserts the
// coercion does NOT touch a genuine tool-call message: OpenAI's own contract
// keeps content null there, and rewriting it would be a spec violation in
// the other direction.
func TestNormalizeChatCompletion_NullContentPreservedWithToolCalls(t *testing.T) {
	body := []byte(`{"id":"gen-toolcall","object":"chat.completion","created":0,"model":"r","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`)

	normalized, _, err := normalizeChatCompletion(body, "hive-free")
	if err != nil {
		t.Fatal(err)
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(normalized, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != nil {
		t.Fatalf("content = %q, want nil (tool-call message must stay null per OpenAI contract)",
			*resp.Choices[0].Message.Content)
	}
}


// TestNormalizeChatCompletion_UsageDetailsAlwaysPresent guards a field that
// used to come and go. prompt_tokens_details and completion_tokens_details
// are part of the shape an OpenAI SDK caller is entitled to, and an upstream
// that omits them made the same assertion pass in one live conformance run
// and fail in the next on the same alias. normalizeReasoningUsage existed for
// this and had no caller anywhere, so nothing was normalized; this test is
// what notices if that call goes missing again.
func TestNormalizeChatCompletion_UsageDetailsAlwaysPresent(t *testing.T) {
	body := []byte(`{"id":"gen-nodetails","object":"chat.completion","created":0,"model":"route-x","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)

	normalized, usage, err := normalizeChatCompletion(body, "hive-small")
	if err != nil {
		t.Fatalf("normalizeChatCompletion: %v", err)
	}
	if usage == nil || usage.PromptTokensDetails == nil || usage.CompletionTokensDetails == nil {
		t.Fatalf("usage details are nil: %+v", usage)
	}

	var out struct {
		Usage struct {
			PromptTokensDetails *struct {
				CachedTokens int64 "json:\"cached_tokens\""
			} "json:\"prompt_tokens_details\""
			CompletionTokensDetails *struct {
				ReasoningTokens int64 "json:\"reasoning_tokens\""
			} "json:\"completion_tokens_details\""
		} "json:\"usage\""
	}
	if err := json.Unmarshal(normalized, &out); err != nil {
		t.Fatalf("unmarshal normalized: %v", err)
	}
	if out.Usage.PromptTokensDetails == nil {
		t.Error("prompt_tokens_details missing from the serialized response")
	} else if out.Usage.PromptTokensDetails.CachedTokens != 0 {
		t.Errorf("cached_tokens = %d, want 0", out.Usage.PromptTokensDetails.CachedTokens)
	}
	if out.Usage.CompletionTokensDetails == nil {
		t.Error("completion_tokens_details missing from the serialized response")
	}
}
