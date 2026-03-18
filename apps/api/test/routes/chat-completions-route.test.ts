import { describe, expect, it, vi } from "vitest";
import { registerChatCompletionsRoute } from "../../src/routes/chat-completions";

type Handler = (request?: any, reply?: any) => Promise<unknown>;

class FakeApp {
  readonly handlers = new Map<string, Handler>();

  post(path: string, optsOrHandler: Handler | Record<string, unknown>, handler?: Handler) {
    this.handlers.set(path, handler ?? (optsOrHandler as Handler));
  }
}

function createReply() {
  let statusCode = 200;
  let sentPayload: unknown;
  const headers = new Map<string, string>();

  return {
    get statusCode() {
      return statusCode;
    },
    get sentPayload() {
      return sentPayload;
    },
    get headers() {
      return headers;
    },
    code(status: number) {
      statusCode = status;
      return this;
    },
    send(payload: unknown) {
      sentPayload = payload;
      return payload;
    },
    header(key: string, value: string) {
      headers.set(key, value);
      return this;
    },
  };
}

describe("chat completions route", () => {
  it("tags bearer-authenticated API-key chat as api traffic from trusted browser origin", async () => {
    const app = new FakeApp();
    const chatCompletions = vi.fn(async (...args: unknown[]) => ({
      statusCode: 200,
      headers: {
        "x-model-routed": "fast-chat",
        "x-provider-used": "ollama",
        "x-provider-model": "llama3.1:8b",
        "x-actual-credits": "10",
      },
      body: { choices: [{ message: { content: "hello" } }] },
    }));
    registerChatCompletionsRoute(app as never, {
      rateLimiter: { allow: async () => true },
      users: { resolveApiKey: async () => ({ userId: "user-1", scopes: ["chat"], apiKeyId: "key-1" }) },
      ai: {
        chatCompletions,
      },
    } as never);

    const handler = app.handlers.get("/v1/chat/completions");
    const reply = createReply();

    await handler?.(
      {
        headers: {
          authorization: "Bearer sk_test_key",
          origin: "http://127.0.0.1:3000",
        },
        body: {
          model: "fast-chat",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(chatCompletions).toHaveBeenCalledWith(
      "user-1",
      { model: "fast-chat", messages: [{ role: "user", content: "hello" }] },
      { channel: "api", apiKeyId: "key-1" },
    );
  });

  it("returns 401 when no authorization header is present", async () => {
    const app = new FakeApp();
    registerChatCompletionsRoute(app as never, {
      rateLimiter: { allow: async () => true },
      users: { resolveApiKey: async () => null },
      ai: { chatCompletions: vi.fn() },
    } as never);

    const handler = app.handlers.get("/v1/chat/completions");
    const reply = createReply();

    await handler?.(
      {
        headers: {},
        body: {
          model: "fast-chat",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(401);
    expect(reply.sentPayload).toEqual({
      error: {
        message: "No API key provided",
        type: "authentication_error",
        param: null,
        code: "invalid_api_key",
      },
    });
  });

  it("tags API-key chat as api traffic and includes the stable api key id", async () => {
    const app = new FakeApp();
    let captured: unknown[] = [];
    registerChatCompletionsRoute(app as never, {
      rateLimiter: { allow: async () => true },
      users: {
        resolveApiKey: async () => ({ userId: "user-1", scopes: ["chat"], apiKeyId: "key-123" }),
      },
      ai: {
        chatCompletions: async (...args: unknown[]) => {
          captured = args;
          return {
            statusCode: 200,
            headers: {
              "x-model-routed": "fast-chat",
              "x-provider-used": "ollama",
              "x-provider-model": "llama3.1:8b",
              "x-actual-credits": "10",
            },
            body: { choices: [{ message: { content: "hello" } }] },
          };
        },
      },
    } as never);

    const handler = app.handlers.get("/v1/chat/completions");
    const reply = createReply();

    await handler?.(
      {
        headers: {
          authorization: "Bearer sk_test",
        },
        body: {
          model: "fast-chat",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(captured).toEqual([
      "user-1",
      { model: "fast-chat", messages: [{ role: "user", content: "hello" }] },
      { channel: "api", apiKeyId: "key-123" },
    ]);
  });

  it("returns 400 when stream is true", async () => {
    const app = new FakeApp();
    registerChatCompletionsRoute(app as never, {
      rateLimiter: { allow: async () => true },
      users: { resolveApiKey: async () => ({ userId: "user-1", scopes: ["chat"], apiKeyId: "key-1" }) },
      ai: { chatCompletions: vi.fn() },
    } as never);

    const handler = app.handlers.get("/v1/chat/completions");
    const reply = createReply();

    await handler?.(
      {
        headers: { authorization: "Bearer sk_test" },
        body: {
          model: "fast-chat",
          messages: [{ role: "user", content: "hello" }],
          stream: true,
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(400);
    expect(reply.sentPayload).toEqual({
      error: {
        message: expect.stringContaining("Streaming"),
        type: "invalid_request_error",
        param: null,
        code: "unsupported_parameter",
      },
    });
  });

  it("passes full request body to service including extra params", async () => {
    const app = new FakeApp();
    const chatCompletions = vi.fn(async () => ({
      statusCode: 200,
      headers: {
        "x-model-routed": "fast-chat",
        "x-provider-used": "openrouter",
        "x-provider-model": "openai/gpt-4o",
        "x-actual-credits": "10",
      },
      body: {
        id: "chatcmpl-test",
        object: "chat.completion",
        created: 1234567890,
        model: "fast-chat",
        choices: [{ index: 0, finish_reason: "stop", message: { role: "assistant", content: "hi" }, logprobs: null }],
        usage: { prompt_tokens: 5, completion_tokens: 3, total_tokens: 8 },
      },
    }));
    registerChatCompletionsRoute(app as never, {
      rateLimiter: { allow: async () => true },
      users: { resolveApiKey: async () => ({ userId: "user-1", scopes: ["chat"], apiKeyId: "key-1" }) },
      ai: { chatCompletions },
    } as never);

    const handler = app.handlers.get("/v1/chat/completions");
    const reply = createReply();

    await handler?.(
      {
        headers: { authorization: "Bearer sk_test" },
        body: {
          model: "fast-chat",
          messages: [{ role: "user", content: "hello" }],
          temperature: 0.7,
          top_p: 0.9,
          max_completion_tokens: 100,
        },
      },
      reply,
    );

    expect(chatCompletions).toHaveBeenCalledWith(
      "user-1",
      {
        model: "fast-chat",
        messages: [{ role: "user", content: "hello" }],
        temperature: 0.7,
        top_p: 0.9,
        max_completion_tokens: 100,
      },
      expect.objectContaining({ channel: "api" }),
    );
  });
});
