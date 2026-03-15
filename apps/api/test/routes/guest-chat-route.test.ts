import { describe, expect, it } from "vitest";
import { registerRoutes } from "../../src/routes";

class FakeApp {
  readonly handlers = new Map<string, (request?: any, reply?: any) => Promise<unknown>>();

  get(path: string, handler: (request?: any, reply?: any) => Promise<unknown>) {
    this.handlers.set(`GET ${path}`, handler);
  }

  post(path: string, handler: (request?: any, reply?: any) => Promise<unknown>) {
    this.handlers.set(`POST ${path}`, handler);
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

describe("guest chat route", () => {
  it("registers an internal guest chat endpoint", () => {
    const app = new FakeApp();

    registerRoutes(app as never, {} as never);

    expect(app.handlers.has("POST /v1/internal/chat/guest")).toBe(true);
  });

  it("allows internal guest chat only with the trusted web token", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    let allowedKey = "";
    const guestChatCompletions = async (...args: unknown[]) => {
      expect(args).toEqual([
        "guest_123",
        "guest-free",
        [{ role: "user", content: "hello" }],
        "203.0.113.10",
      ]);
      return {
        statusCode: 200,
        body: { choices: [{ message: { content: "Guest reply" } }] },
      };
    };
    const app = new FakeApp();
    registerRoutes(app as never, {
      ai: {
        guestChatCompletions,
      },
      rateLimiter: {
        allow: async (key: string) => {
          allowedKey = key;
          return true;
        },
      },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/chat/guest");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          "x-web-guest-token": "test-web-token",
          "x-guest-id": "guest_123",
          "x-guest-client-ip": "203.0.113.10",
        },
        body: {
          model: "guest-free",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(200);
    expect(allowedKey).toBe("guest:guest_123:203.0.113.10");
    expect(result).toEqual({ choices: [{ message: { content: "Guest reply" } }] });
  });

  it("forwards provider headers from guest-free completions", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    const app = new FakeApp();
    registerRoutes(app as never, {
      ai: {
        guestChatCompletions: async () => ({
          statusCode: 200,
          headers: {
            "x-model-routed": "guest-free",
            "x-provider-used": "openrouter",
            "x-provider-model": "openrouter/free-model",
            "x-actual-credits": "0",
          },
          body: { choices: [{ message: { content: "Provider reply" } }] },
        }),
      },
      rateLimiter: {
        allow: async () => true,
      },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/chat/guest");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          "x-web-guest-token": "test-web-token",
          "x-guest-id": "guest_123",
          "x-guest-client-ip": "203.0.113.10",
        },
        body: {
          model: "guest-free",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(reply.headers.get("x-model-routed")).toBe("guest-free");
    expect(reply.headers.get("x-provider-used")).toBe("openrouter");
    expect(reply.headers.get("x-provider-model")).toBe("openrouter/free-model");
    expect(reply.headers.get("x-actual-credits")).toBe("0");
    expect(result).toEqual({ choices: [{ message: { content: "Provider reply" } }] });
  });

  it("rejects internal guest chat when the forwarded guest identity is missing", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    const app = new FakeApp();
    registerRoutes(app as never, {
      ai: {
        guestChatCompletions: async () => ({
          statusCode: 200,
          body: { choices: [{ message: { content: "Guest reply" } }] },
        }),
      },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/chat/guest");
    const reply = createReply();

    await handler?.(
      {
        headers: {
          "x-web-guest-token": "test-web-token",
        },
        body: {
          model: "guest-free",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(403);
    expect(reply.sentPayload).toEqual({ error: "forbidden" });
  });

  it("rejects internal guest chat when the trusted web token is missing", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    const app = new FakeApp();
    registerRoutes(app as never, {
      ai: {
        guestChatCompletions: async () => ({
          statusCode: 200,
          body: { choices: [{ message: { content: "Guest reply" } }] },
        }),
      },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/chat/guest");
    const reply = createReply();

    await handler?.(
      {
        headers: {},
        body: {
          model: "guest-free",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(403);
    expect(reply.sentPayload).toEqual({ error: "forbidden" });
  });

  it("rejects internal guest chat for paid models even with the trusted web token", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    const app = new FakeApp();
    registerRoutes(app as never, {
      ai: {
        guestChatCompletions: async () => ({
          statusCode: 403,
          error: "forbidden",
        }),
      },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/chat/guest");
    const reply = createReply();

    await handler?.(
      {
        headers: {
          "x-web-guest-token": "test-web-token",
          "x-guest-id": "guest_123",
        },
        body: {
          model: "smart-reasoning",
          messages: [{ role: "user", content: "hello" }],
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(403);
    expect(reply.sentPayload).toEqual({ error: "forbidden" });
  });
});
