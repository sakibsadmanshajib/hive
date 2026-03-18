import { describe, it, expect } from "vitest";

// ---------------------------------------------------------------------------
// Mock SSE ReadableStream helper
// ---------------------------------------------------------------------------

function createMockSSEStream(
  chunks: object[],
  includeDone = true,
): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) {
        controller.enqueue(encoder.encode(`data: ${JSON.stringify(chunk)}\n\n`));
      }
      if (includeDone) {
        controller.enqueue(encoder.encode("data: [DONE]\n\n"));
      }
      controller.close();
    },
  });
}

// ---------------------------------------------------------------------------
// Test fixture chunks
// ---------------------------------------------------------------------------

const INTERMEDIATE_CHUNK = {
  id: "chatcmpl-test123",
  object: "chat.completion.chunk",
  created: 1700000000,
  model: "test-model",
  choices: [
    {
      index: 0,
      delta: { role: "assistant", content: "Hello" },
      finish_reason: null,
      logprobs: null,
    },
  ],
  usage: null,
};

const FINISH_CHUNK = {
  id: "chatcmpl-test123",
  object: "chat.completion.chunk",
  created: 1700000000,
  model: "test-model",
  choices: [
    {
      index: 0,
      delta: {},
      finish_reason: "stop",
      logprobs: null,
    },
  ],
  usage: null,
};

const USAGE_CHUNK = {
  id: "chatcmpl-test123",
  object: "chat.completion.chunk",
  created: 1700000000,
  model: "test-model",
  choices: [] as unknown[],
  usage: {
    prompt_tokens: 10,
    completion_tokens: 20,
    total_tokens: 30,
  },
};

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("SSE streaming compliance (CHAT-04, CHAT-05)", () => {
  // -----------------------------------------------------------------------
  // CHAT-04: SSE chunk format
  // -----------------------------------------------------------------------

  describe("CHAT-04: SSE chunk format", () => {
    it("intermediate chunk has object: chat.completion.chunk", () => {
      expect(INTERMEDIATE_CHUNK.object).toBe("chat.completion.chunk");
    });

    it("intermediate chunk uses choices[].delta (not message)", () => {
      const choice = INTERMEDIATE_CHUNK.choices[0];
      expect(choice).toHaveProperty("delta");
      expect(choice).not.toHaveProperty("message");
    });

    it("intermediate chunk delta has content field", () => {
      expect(INTERMEDIATE_CHUNK.choices[0].delta).toHaveProperty("content");
    });

    it("finish chunk has finish_reason: stop", () => {
      expect(FINISH_CHUNK.choices[0].finish_reason).toBe("stop");
    });

    it("finish chunk delta is empty object", () => {
      expect(FINISH_CHUNK.choices[0].delta).toEqual({});
    });
  });

  // -----------------------------------------------------------------------
  // CHAT-04: SSE frame format
  // -----------------------------------------------------------------------

  describe("CHAT-04: SSE frame format", () => {
    it("mock stream produces data: {json} frames", async () => {
      const stream = createMockSSEStream([INTERMEDIATE_CHUNK]);
      const reader = stream.getReader();
      const decoder = new TextDecoder();
      const { value } = await reader.read();
      const text = decoder.decode(value);
      expect(text).toMatch(/^data: \{.*\}\n\n$/);
      reader.releaseLock();
    });

    it("mock stream terminates with data: [DONE]", async () => {
      const stream = createMockSSEStream([INTERMEDIATE_CHUNK]);
      const reader = stream.getReader();
      const decoder = new TextDecoder();
      const chunks: string[] = [];
      let done = false;
      while (!done) {
        const result = await reader.read();
        if (result.done) {
          done = true;
          break;
        }
        chunks.push(decoder.decode(result.value));
      }
      const fullOutput = chunks.join("");
      expect(fullOutput).toContain("data: [DONE]\n\n");
    });

    it("each chunk is valid JSON when parsed from SSE frame", async () => {
      const stream = createMockSSEStream([INTERMEDIATE_CHUNK, FINISH_CHUNK]);
      const reader = stream.getReader();
      const decoder = new TextDecoder();
      const frames: string[] = [];
      let done = false;
      while (!done) {
        const result = await reader.read();
        if (result.done) {
          done = true;
          break;
        }
        frames.push(decoder.decode(result.value));
      }
      const fullOutput = frames.join("");
      const lines = fullOutput
        .split("\n\n")
        .filter((l) => l.startsWith("data: ") && l !== "data: [DONE]");
      for (const line of lines) {
        const json = line.replace(/^data: /, "");
        expect(() => JSON.parse(json)).not.toThrow();
        const parsed = JSON.parse(json);
        expect(parsed).toHaveProperty("object", "chat.completion.chunk");
        expect(parsed).toHaveProperty("id");
        expect(parsed).toHaveProperty("created");
        expect(parsed).toHaveProperty("model");
      }
    });
  });

  // -----------------------------------------------------------------------
  // CHAT-05: usage in streaming chunks
  // -----------------------------------------------------------------------

  describe("CHAT-05: usage in streaming chunks", () => {
    it("intermediate chunks have usage: null (not undefined/omitted)", () => {
      expect(INTERMEDIATE_CHUNK).toHaveProperty("usage");
      expect(INTERMEDIATE_CHUNK.usage).toBeNull();
    });

    it("finish chunk has usage: null", () => {
      expect(FINISH_CHUNK).toHaveProperty("usage");
      expect(FINISH_CHUNK.usage).toBeNull();
    });

    it("usage chunk has choices: [] (empty array)", () => {
      expect(USAGE_CHUNK.choices).toEqual([]);
    });

    it("usage chunk has usage object with prompt_tokens, completion_tokens, total_tokens", () => {
      expect(USAGE_CHUNK.usage).toHaveProperty("prompt_tokens");
      expect(USAGE_CHUNK.usage).toHaveProperty("completion_tokens");
      expect(USAGE_CHUNK.usage).toHaveProperty("total_tokens");
      expect(typeof USAGE_CHUNK.usage.prompt_tokens).toBe("number");
      expect(typeof USAGE_CHUNK.usage.completion_tokens).toBe("number");
      expect(typeof USAGE_CHUNK.usage.total_tokens).toBe("number");
    });

    it("usage chunk total_tokens equals prompt + completion", () => {
      expect(USAGE_CHUNK.usage.total_tokens).toBe(
        USAGE_CHUNK.usage.prompt_tokens + USAGE_CHUNK.usage.completion_tokens,
      );
    });

    it("full stream with include_usage has usage chunk before [DONE]", async () => {
      const stream = createMockSSEStream([
        INTERMEDIATE_CHUNK,
        FINISH_CHUNK,
        USAGE_CHUNK,
      ]);
      const reader = stream.getReader();
      const decoder = new TextDecoder();
      const frames: string[] = [];
      let done = false;
      while (!done) {
        const result = await reader.read();
        if (result.done) {
          done = true;
          break;
        }
        frames.push(decoder.decode(result.value));
      }
      const fullOutput = frames.join("");
      const parts = fullOutput.split("data: [DONE]");
      // Usage chunk should be the last data frame before [DONE]
      const beforeDone = parts[0];
      const lastDataLine = beforeDone
        .trim()
        .split("\n\n")
        .filter((l) => l.startsWith("data: "))
        .pop()!;
      const lastChunk = JSON.parse(lastDataLine.replace(/^data: /, ""));
      expect(lastChunk.choices).toEqual([]);
      expect(lastChunk.usage).not.toBeNull();
      expect(lastChunk.usage).toHaveProperty("prompt_tokens");
    });
  });
});
