import { describe, it, expect, beforeAll, afterAll } from "vitest";
import OpenAI from "openai";
import type { FastifyInstance } from "fastify";
import { createTestApp, createMockServices } from "./helpers/test-app";

type OpenAiErrorBody = { error: { type: string; code: string | null; param: null; message: string } };

let app: FastifyInstance;
let baseUrl: string;

beforeAll(async () => {
  const result = await createTestApp(createMockServices("valid-api-key", "user-1"));
  app = result.app;
  baseUrl = result.address;
});

afterAll(async () => {
  await app.close();
});

describe("OpenAI SDK regression tests", () => {
  // ─── Existing auth / stub tests ───────────────────────────────────────────

  it("models.list() with an invalid key throws AuthenticationError", async () => {
    const client = new OpenAI({ apiKey: "invalid-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const request = client.models.list();
    await expect(request).rejects.toBeInstanceOf(OpenAI.AuthenticationError);
    await expect(request).rejects.toMatchObject({ status: 401 });
  });

  it("chat.completions.create() with invalid key throws AuthenticationError", async () => {
    const client = new OpenAI({ apiKey: "invalid-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const request = client.chat.completions.create({
      model: "mock-chat",
      messages: [{ role: "user" as const, content: "hi" }],
    });
    await expect(request).rejects.toBeInstanceOf(OpenAI.AuthenticationError);
    await expect(request).rejects.toMatchObject({ status: 401 });
  });

  it("POST /v1/audio/speech returns 404 with unsupported_endpoint", async () => {
    const response = await fetch(`${baseUrl}/v1/audio/speech`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: "{}",
    });
    expect(response.status).toBe(404);
    const body: OpenAiErrorBody = await response.json();
    expect(body.error.code).toBe("unsupported_endpoint");
  });

  it("GET /v1/files returns 404 with stub error format", async () => {
    const response = await fetch(`${baseUrl}/v1/files`);
    expect(response.status).toBe(404);
    const body: OpenAiErrorBody = await response.json();
    expect(body.error.code).toBe("unsupported_endpoint");
    expect(body.error.type).toBe("not_found_error");
    expect(body.error.param).toBeNull();
  });

  it("POST /v1/chat/completions with invalid key returns 401 authentication_error", async () => {
    const response = await fetch(`${baseUrl}/v1/chat/completions`, {
      method: "POST",
      headers: {
        "authorization": "Bearer invalid",
        "content-type": "application/json",
      },
      body: JSON.stringify({ model: "mock-chat", messages: [{ role: "user", content: "hi" }] }),
    });
    expect(response.status).toBe(401);
    const body: OpenAiErrorBody = await response.json();
    expect(body.error.type).toBe("authentication_error");
  });

  // ─── Success-path tests ────────────────────────────────────────────────────

  it("models.retrieve('mock-chat') returns the correct model object", async () => {
    const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const model = await client.models.retrieve("mock-chat");
    expect(model.id).toBe("mock-chat");
    expect(model.object).toBe("model");
  });

  it("models.retrieve('nonexistent-model') throws NotFoundError (404)", async () => {
    const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const request = client.models.retrieve("nonexistent-model-id");
    await expect(request).rejects.toBeInstanceOf(OpenAI.NotFoundError);
    await expect(request).rejects.toMatchObject({ status: 404 });
  });

  it("chat.completions.create() returns a valid completion with correct shape", async () => {
    const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const completion = await client.chat.completions.create({
      model: "mock-chat",
      messages: [{ role: "user" as const, content: "Say hello" }],
    });
    expect(completion.object).toBe("chat.completion");
    expect(typeof completion.id).toBe("string");
    expect(completion.id.startsWith("chatcmpl-")).toBe(true);
    expect(Array.isArray(completion.choices)).toBe(true);
    expect(completion.choices.length).toBeGreaterThan(0);
    const firstChoice = completion.choices[0];
    expect(firstChoice).toBeDefined();
    if (firstChoice) {
      expect(typeof firstChoice.message.content).toBe("string");
      expect(firstChoice.message.role).toBe("assistant");
    }
  });

  it("embeddings.create() returns a list of embeddings", async () => {
    const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const result = await client.embeddings.create({
      model: "text-embedding-3-small",
      input: "hello world",
    });
    expect(result.object).toBe("list");
    expect(Array.isArray(result.data)).toBe(true);
    expect(result.data.length).toBeGreaterThan(0);
    const firstEmbedding = result.data[0];
    expect(firstEmbedding).toBeDefined();
    if (firstEmbedding) {
      expect(firstEmbedding.object).toBe("embedding");
      expect(Array.isArray(firstEmbedding.embedding)).toBe(true);
    }
  });

  it("images.generate() returns a URL or b64_json", async () => {
    const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const result = await client.images.generate({
      model: "dall-e-3",
      prompt: "a cat sitting on a mat",
    });
    expect(Array.isArray(result.data)).toBe(true);
    expect(result.data.length).toBeGreaterThan(0);
    const firstImage = result.data[0];
    expect(firstImage).toBeDefined();
    if (firstImage) {
      const hasUrlOrB64 = typeof firstImage.url === "string" || typeof firstImage.b64_json === "string";
      expect(hasUrlOrB64).toBe(true);
    }
  });

  it("POST /v1/responses returns a valid Responses API object", async () => {
    const response = await fetch(`${baseUrl}/v1/responses`, {
      method: "POST",
      headers: {
        "authorization": "Bearer valid-api-key",
        "content-type": "application/json",
      },
      body: JSON.stringify({
        model: "mock-chat",
        input: "What is 2 + 2?",
      }),
    });
    expect(response.status).toBe(200);
    const body: {
      id: string;
      object: string;
      status: string;
      output: Array<{ type: string; role: string; content: Array<{ type: string; text: string }> }>;
      usage: { input_tokens: number; output_tokens: number };
    } = await response.json();
    expect(body.object).toBe("response");
    expect(body.id.startsWith("resp_")).toBe(true);
    expect(body.status).toBe("completed");
    expect(Array.isArray(body.output)).toBe(true);
    expect(body.output.length).toBeGreaterThan(0);
    const firstOutput = body.output[0];
    expect(firstOutput).toBeDefined();
    if (firstOutput) {
      expect(firstOutput.type).toBe("message");
      expect(firstOutput.role).toBe("assistant");
    }
    expect(typeof body.usage.input_tokens).toBe("number");
    expect(typeof body.usage.output_tokens).toBe("number");
  });
});

// ─── Streaming test ───────────────────────────────────────────────────────────

describe("Streaming regression tests", () => {
  it("chat.completions.create({ stream: true }) yields chunks via async iterator", async () => {
    const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const stream = await client.chat.completions.create({
      model: "mock-chat",
      messages: [{ role: "user" as const, content: "Stream something" }],
      stream: true,
    });

    const chunks: string[] = [];
    let chunkCount = 0;
    for await (const chunk of stream) {
      chunkCount++;
      const delta = chunk.choices[0]?.delta?.content;
      if (typeof delta === "string") {
        chunks.push(delta);
      }
    }

    expect(chunkCount).toBeGreaterThanOrEqual(2);
    const combined = chunks.join("");
    expect(combined.length).toBeGreaterThan(0);
  });
});

// ─── Error-path tests ─────────────────────────────────────────────────────────

describe("Error-path regression tests", () => {
  it("402 Insufficient Credits — SDK receives APIError with status 402", async () => {
    const { app: errorApp, address: errorBaseUrl } = await createTestApp(
      createMockServices("valid-api-key", "user-1", {
        chatCompletions: async () => ({ error: "insufficient credits", statusCode: 402 as const }),
      }),
    );

    try {
      const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${errorBaseUrl}/v1`, maxRetries: 0 });
      const request = client.chat.completions.create({
        model: "mock-chat",
        messages: [{ role: "user" as const, content: "hi" }],
      });
      await expect(request).rejects.toBeInstanceOf(OpenAI.APIError);
      await expect(request).rejects.toMatchObject({ status: 402 });
    } finally {
      await errorApp.close();
    }
  });

  it("429 Rate Limit — SDK throws RateLimitError", async () => {
    const { app: rateLimitApp, address: rateLimitBaseUrl } = await createTestApp(
      createMockServices("valid-api-key", "user-1", undefined, { allow: async () => false }),
    );

    try {
      const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${rateLimitBaseUrl}/v1`, maxRetries: 0 });
      const request = client.chat.completions.create({
        model: "mock-chat",
        messages: [{ role: "user" as const, content: "hi" }],
      });
      await expect(request).rejects.toBeInstanceOf(OpenAI.RateLimitError);
      await expect(request).rejects.toMatchObject({ status: 429 });
    } finally {
      await rateLimitApp.close();
    }
  });

  it("422 Validation — SDK throws error when service returns 422-equivalent status", async () => {
    const { app: validationApp, address: validationBaseUrl } = await createTestApp(
      createMockServices("valid-api-key", "user-1", {
        chatCompletions: async () => ({
          error: "invalid request: model field is required",
          statusCode: 400 as const,
        }),
      }),
    );

    try {
      const client = new OpenAI({ apiKey: "valid-api-key", baseURL: `${validationBaseUrl}/v1`, maxRetries: 0 });
      const request = client.chat.completions.create({
        model: "mock-chat",
        messages: [{ role: "user" as const, content: "hi" }],
      });
      await expect(request).rejects.toBeInstanceOf(OpenAI.BadRequestError);
      await expect(request).rejects.toMatchObject({ status: 400 });
    } finally {
      await validationApp.close();
    }
  });
});
