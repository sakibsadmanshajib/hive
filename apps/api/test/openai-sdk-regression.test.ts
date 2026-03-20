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
  it("models.list() with any key returns a model list (public endpoint)", async () => {
    const client = new OpenAI({ apiKey: "invalid-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    const result = await client.models.list();
    expect(result.object).toBe("list");
    expect(Array.isArray(result.data)).toBe(true);
  });

  it("chat.completions.create() with invalid key throws AuthenticationError", async () => {
    const client = new OpenAI({ apiKey: "invalid-key", baseURL: `${baseUrl}/v1`, maxRetries: 0 });
    let caught: unknown = null;
    await client.chat.completions.create({
      model: "mock-chat",
      messages: [{ role: "user" as const, content: "hi" }],
    }).catch((err: unknown) => { caught = err; });
    expect(caught).toBeInstanceOf(OpenAI.AuthenticationError);
    if (caught instanceof OpenAI.AuthenticationError) {
      expect(caught.status).toBe(401);
    }
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
});
