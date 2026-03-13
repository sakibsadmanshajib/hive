import { describe, expect, it } from "vitest";
import { registerChatCompletionsRoute } from "../../src/routes/chat-completions";

type Handler = (request?: any, reply?: any) => Promise<unknown>;

class FakeApp {
  readonly handlers = new Map<string, Handler>();

  post(path: string, handler: Handler) {
    this.handlers.set(path, handler);
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
  it("tags authenticated browser chat as web analytics traffic", async () => {
    const app = new FakeApp();
    let captured: unknown[] = [];
    registerChatCompletionsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
      supabaseAuth: { getSessionPrincipal: async () => ({ userId: "user-1" }) },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      rateLimiter: { allow: async () => true },
      users: { resolveApiKey: async () => null },
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
          authorization: "Bearer session-token",
          origin: "http://127.0.0.1:3000",
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
      "fast-chat",
      [{ role: "user", content: "hello" }],
      { channel: "web" },
    ]);
  });

  it("keeps session-authenticated traffic on the api channel when no trusted browser origin is present", async () => {
    const app = new FakeApp();
    let captured: unknown[] = [];
    registerChatCompletionsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
      supabaseAuth: { getSessionPrincipal: async () => ({ userId: "user-1" }) },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      rateLimiter: { allow: async () => true },
      users: { resolveApiKey: async () => null },
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
          authorization: "Bearer session-token",
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
      "fast-chat",
      [{ role: "user", content: "hello" }],
      { channel: "api", apiKeyId: undefined },
    ]);
  });

  it("tags API-key chat as api traffic and includes the stable api key id", async () => {
    const app = new FakeApp();
    let captured: unknown[] = [];
    registerChatCompletionsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: false } } },
      supabaseAuth: { getSessionPrincipal: async () => null },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
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
          "x-api-key": "sk_test",
          origin: "http://127.0.0.1:3000",
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
      "fast-chat",
      [{ role: "user", content: "hello" }],
      { channel: "api", apiKeyId: "key-123" },
    ]);
  });
});
