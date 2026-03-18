import { describe, expect, it } from "vitest";

import { registerResponsesRoute } from "../../src/routes/responses";

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

describe("responses route", () => {
  it("forwards routing and billing headers from the runtime response", async () => {
    const app = new FakeApp();
    registerResponsesRoute(app as never, {
      rateLimiter: { allow: async () => true },
      users: { resolveApiKey: async () => ({ userId: "user-1", scopes: ["chat"], apiKeyId: "key-1" }) },
      ai: {
        responses: async () => ({
          statusCode: 200,
          headers: {
            "x-model-routed": "fast-chat",
            "x-provider-used": "ollama",
            "x-provider-model": "llama3.1:8b",
            "x-actual-credits": "7",
          },
          body: {
            id: "resp_123",
            object: "response",
            model: "fast-chat",
            output: [{ type: "text", text: "hello" }],
          },
        }),
      },
    } as never);

    const handler = app.handlers.get("/v1/responses");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          authorization: "Bearer sk_test_key",
        },
        body: {
          input: "hello",
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(200);
    expect(reply.headers.get("x-model-routed")).toBe("fast-chat");
    expect(reply.headers.get("x-provider-used")).toBe("ollama");
    expect(reply.headers.get("x-provider-model")).toBe("llama3.1:8b");
    expect(reply.headers.get("x-actual-credits")).toBe("7");
    expect(result).toEqual({
      id: "resp_123",
      object: "response",
      model: "fast-chat",
      output: [{ type: "text", text: "hello" }],
    });
  });
});
