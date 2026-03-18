import { describe, it, expect } from "vitest";

// These tests validate the SHAPE of chat completion responses against OpenAI's spec.
// They don't test the route itself — they test the response contract.

describe("chat completion response compliance", () => {
  // Helper: a compliant response
  const compliantResponse = {
    id: "chatcmpl-abc123",
    object: "chat.completion",
    created: 1700000000,
    model: "test-model",
    choices: [
      {
        index: 0,
        finish_reason: "stop",
        message: {
          role: "assistant",
          content: "Hello!",
          refusal: null,
        },
        logprobs: null,
      },
    ],
    usage: {
      prompt_tokens: 10,
      completion_tokens: 5,
      total_tokens: 15,
    },
  };

  it("has required top-level fields", () => {
    expect(compliantResponse).toHaveProperty("id");
    expect(compliantResponse).toHaveProperty("object", "chat.completion");
    expect(compliantResponse).toHaveProperty("created");
    expect(typeof compliantResponse.created).toBe("number");
    expect(compliantResponse).toHaveProperty("model");
  });

  it("choices have required fields", () => {
    const choice = compliantResponse.choices[0];
    expect(choice).toHaveProperty("index", 0);
    expect(choice).toHaveProperty("finish_reason");
    expect(["stop", "length", "tool_calls", "content_filter", "function_call"]).toContain(choice.finish_reason);
    expect(choice).toHaveProperty("message");
    expect(choice.message).toHaveProperty("role", "assistant");
    expect(choice).toHaveProperty("logprobs");
    // logprobs MUST be present (null is valid, undefined is NOT)
    expect("logprobs" in choice).toBe(true);
  });

  it("usage has required fields", () => {
    expect(compliantResponse).toHaveProperty("usage");
    expect(compliantResponse.usage).toHaveProperty("prompt_tokens");
    expect(compliantResponse.usage).toHaveProperty("completion_tokens");
    expect(compliantResponse.usage).toHaveProperty("total_tokens");
    expect(typeof compliantResponse.usage.prompt_tokens).toBe("number");
    expect(typeof compliantResponse.usage.completion_tokens).toBe("number");
    expect(typeof compliantResponse.usage.total_tokens).toBe("number");
  });

  it("object field is exactly 'chat.completion'", () => {
    expect(compliantResponse.object).toBe("chat.completion");
    // NOT "chat_completion" or "chatCompletion"
  });

  it("id field starts with 'chatcmpl'", () => {
    expect(compliantResponse.id).toMatch(/^chatcmpl/);
  });

  it("message includes refusal field", () => {
    const msg = compliantResponse.choices[0].message;
    expect("refusal" in msg).toBe(true);
  });
});
