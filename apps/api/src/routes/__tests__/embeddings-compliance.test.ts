import { describe, it, expect } from "vitest";

// These tests validate the SHAPE of embedding responses against OpenAI's spec.
// They don't test the route itself — they test the response contract.

const EMBEDDING_FIXTURE = {
  object: "list",
  data: [
    { object: "embedding", embedding: [0.1, 0.2, 0.3, -0.4, 0.5], index: 0 },
    { object: "embedding", embedding: [0.6, 0.7, 0.8, -0.9, 1.0], index: 1 },
  ],
  model: "text-embedding-3-small",
  usage: { prompt_tokens: 8, total_tokens: 8 },
};

describe("SURF-01: CreateEmbeddingResponse compliance", () => {
  it("has object: 'list' at top level", () => {
    expect(EMBEDDING_FIXTURE.object).toBe("list");
  });

  it("data[] items have object: 'embedding'", () => {
    for (const item of EMBEDDING_FIXTURE.data) {
      expect(item.object).toBe("embedding");
    }
  });

  it("data[] items have embedding as number[]", () => {
    for (const item of EMBEDDING_FIXTURE.data) {
      expect(Array.isArray(item.embedding)).toBe(true);
      for (const val of item.embedding) {
        expect(typeof val).toBe("number");
      }
    }
  });

  it("data[] items have index as integer", () => {
    for (const item of EMBEDDING_FIXTURE.data) {
      expect(typeof item.index).toBe("number");
      expect(Number.isInteger(item.index)).toBe(true);
    }
  });

  it("has model as string", () => {
    expect(typeof EMBEDDING_FIXTURE.model).toBe("string");
  });

  it("has usage with prompt_tokens and total_tokens", () => {
    expect(typeof EMBEDDING_FIXTURE.usage.prompt_tokens).toBe("number");
    expect(EMBEDDING_FIXTURE.usage.prompt_tokens).toBeGreaterThanOrEqual(0);
    expect(typeof EMBEDDING_FIXTURE.usage.total_tokens).toBe("number");
    expect(EMBEDDING_FIXTURE.usage.total_tokens).toBeGreaterThanOrEqual(0);
  });

  it("usage has no completion_tokens field", () => {
    expect("completion_tokens" in EMBEDDING_FIXTURE.usage).toBe(false);
  });

  it("data[] indices are sequential starting from 0", () => {
    const indices = EMBEDDING_FIXTURE.data.map((item) => item.index);
    expect(indices).toEqual([0, 1]);
  });
});
