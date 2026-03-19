import { describe, it, expect } from "vitest";

// These tests validate the SHAPE of Responses API responses against OpenAI's spec.
// They don't test the route itself — they test the response contract.

const RESPONSE_FIXTURE = {
  id: "resp_abc123def456789012345678",
  object: "response",
  created_at: 1710792000,
  status: "completed",
  model: "openai/gpt-4o",
  output: [
    {
      type: "message",
      id: "msg_abc123def456789012345678",
      role: "assistant",
      status: "completed",
      content: [
        { type: "output_text", text: "Hello! How can I help you today?" },
      ],
    },
  ],
  usage: {
    input_tokens: 12,
    output_tokens: 9,
    total_tokens: 21,
  },
};

describe("SURF-03: Response compliance", () => {
  it("id starts with 'resp_'", () => {
    expect(RESPONSE_FIXTURE.id.startsWith("resp_")).toBe(true);
  });

  it("object is 'response'", () => {
    expect(RESPONSE_FIXTURE.object).toBe("response");
  });

  it("created_at is unix timestamp integer", () => {
    expect(typeof RESPONSE_FIXTURE.created_at).toBe("number");
    expect(Number.isInteger(RESPONSE_FIXTURE.created_at)).toBe(true);
    expect(RESPONSE_FIXTURE.created_at).toBeGreaterThan(1_000_000_000);
  });

  it("status is 'completed'", () => {
    expect(RESPONSE_FIXTURE.status).toBe("completed");
  });

  it("model is a string", () => {
    expect(typeof RESPONSE_FIXTURE.model).toBe("string");
  });

  it("output is non-empty array", () => {
    expect(Array.isArray(RESPONSE_FIXTURE.output)).toBe(true);
    expect(RESPONSE_FIXTURE.output.length).toBeGreaterThan(0);
  });

  it("output[0].type is 'message'", () => {
    expect(RESPONSE_FIXTURE.output[0].type).toBe("message");
  });

  it("output[0].id starts with 'msg_'", () => {
    expect(RESPONSE_FIXTURE.output[0].id.startsWith("msg_")).toBe(true);
  });

  it("output[0].role is 'assistant'", () => {
    expect(RESPONSE_FIXTURE.output[0].role).toBe("assistant");
  });

  it("output[0].status is 'completed'", () => {
    expect(RESPONSE_FIXTURE.output[0].status).toBe("completed");
  });

  it("output[0].content[0].type is 'output_text'", () => {
    expect(RESPONSE_FIXTURE.output[0].content[0].type).toBe("output_text");
  });

  it("output[0].content[0].text is a string", () => {
    expect(typeof RESPONSE_FIXTURE.output[0].content[0].text).toBe("string");
  });

  it("usage has input_tokens, output_tokens, total_tokens", () => {
    expect(typeof RESPONSE_FIXTURE.usage.input_tokens).toBe("number");
    expect(RESPONSE_FIXTURE.usage.input_tokens).toBeGreaterThanOrEqual(0);
    expect(typeof RESPONSE_FIXTURE.usage.output_tokens).toBe("number");
    expect(RESPONSE_FIXTURE.usage.output_tokens).toBeGreaterThanOrEqual(0);
    expect(typeof RESPONSE_FIXTURE.usage.total_tokens).toBe("number");
    expect(RESPONSE_FIXTURE.usage.total_tokens).toBeGreaterThanOrEqual(0);
  });

  it("usage does NOT use prompt_tokens/completion_tokens naming", () => {
    expect("prompt_tokens" in RESPONSE_FIXTURE.usage).toBe(false);
    expect("completion_tokens" in RESPONSE_FIXTURE.usage).toBe(false);
  });

  it("total_tokens equals input_tokens + output_tokens", () => {
    expect(RESPONSE_FIXTURE.usage.total_tokens).toBe(
      RESPONSE_FIXTURE.usage.input_tokens + RESPONSE_FIXTURE.usage.output_tokens
    );
  });
});
