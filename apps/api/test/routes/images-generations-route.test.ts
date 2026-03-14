import { describe, expect, it, vi } from "vitest";
import { registerImagesGenerationsRoute } from "../../src/routes/images-generations";

type Handler = (
  request?: { headers?: Record<string, string>; body?: unknown },
  reply?: {
    code: (status: number) => unknown;
    send: (payload: unknown) => unknown;
    header: (key: string, value: string) => unknown;
  },
) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, Handler>();

  post(path: string, handler: Handler) {
    this.handlers.set(`POST ${path}`, handler);
  }
}

describe("images generations route", () => {
  it("forwards an OpenAI-compatible image request payload to the runtime service", async () => {
    const imageGeneration = vi.fn(async () => ({
      statusCode: 200,
      headers: {
        "x-model-routed": "image-basic",
        "x-actual-credits": "120",
        "x-provider-used": "openai",
        "x-provider-model": "gpt-image-1",
      },
      body: {
        created: 1_710_000_000,
        data: [{ url: "https://cdn.example.com/image.png" }],
      },
    }));
    const app = new FakeApp();

    registerImagesGenerationsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["image"] }) },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: true, twoFactorEnabled: false }),
        canUse: vi.fn((key: string, settings: Record<string, boolean>) => settings[key]),
      },
      rateLimiter: { allow: async () => true },
      ai: { imageGeneration },
    } as never);

    const handler = app.handlers.get("POST /v1/images/generations");
    const headers = new Map<string, string>();
    let statusCode = 200;
    const reply = {
      code: (code: number) => {
        statusCode = code;
        return {
          send: (body: unknown) => body,
        };
      },
      send: (body: unknown) => body,
      header: (key: string, value: string) => {
        headers.set(key, value);
        return reply;
      },
    };
    const payload = await handler?.(
      {
        headers: { "x-api-key": "sk_1" },
        body: {
          model: "image-basic",
          prompt: "a lighthouse in fog",
          n: 1,
          size: "1024x1024",
          response_format: "url",
          user: "user-123",
        },
      },
      reply,
    );

    expect(imageGeneration).toHaveBeenCalledWith("user_1", {
      model: "image-basic",
      prompt: "a lighthouse in fog",
      n: 1,
      size: "1024x1024",
      responseFormat: "url",
      user: "user-123",
    }, {
      channel: "api",
      apiKeyId: undefined,
    });
    expect(statusCode).toBe(200);
    expect(headers.get("x-model-routed")).toBe("image-basic");
    expect(headers.get("x-actual-credits")).toBe("120");
    expect(headers.get("x-provider-used")).toBe("openai");
    expect(headers.get("x-provider-model")).toBe("gpt-image-1");
    expect(payload).toEqual({
      created: 1_710_000_000,
      data: [{ url: "https://cdn.example.com/image.png" }],
    });
  });

  it("forwards the resolved stable api key id into image generation usage context", async () => {
    const imageGeneration = vi.fn(async () => ({
      statusCode: 200,
      headers: {
        "x-model-routed": "image-basic",
        "x-actual-credits": "120",
        "x-provider-used": "openai",
        "x-provider-model": "gpt-image-1",
      },
      body: {
        created: 1_710_000_000,
        data: [{ url: "https://cdn.example.com/image.png" }],
      },
    }));
    const app = new FakeApp();

    registerImagesGenerationsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["image"], apiKeyId: "key_123" }) },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: true, twoFactorEnabled: false }),
        canUse: vi.fn((key: string, settings: Record<string, boolean>) => settings[key]),
      },
      rateLimiter: { allow: async () => true },
      ai: { imageGeneration },
    } as never);

    const handler = app.handlers.get("POST /v1/images/generations");
    const reply = {
      code: (_code: number) => ({
        send: (body: unknown) => body,
      }),
      send: (body: unknown) => body,
      header: () => reply,
    };

    await handler?.(
      {
        headers: { "x-api-key": "sk_1" },
        body: {
          model: "image-basic",
          prompt: "a lighthouse in fog",
          response_format: "url",
        },
      },
      reply,
    );

    expect(imageGeneration).toHaveBeenCalledWith(
      "user_1",
      expect.objectContaining({
        model: "image-basic",
        prompt: "a lighthouse in fog",
      }),
      {
        channel: "api",
        apiKeyId: "key_123",
      },
    );
  });

  it("returns a sanitized provider failure without leaking internal diagnostics", async () => {
    const app = new FakeApp();

    registerImagesGenerationsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["image"] }) },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: true, twoFactorEnabled: false }),
        canUse: vi.fn((key: string, settings: Record<string, boolean>) => settings[key]),
      },
      rateLimiter: { allow: async () => true },
      ai: {
        imageGeneration: vi.fn(async () => ({
          statusCode: 502,
          error: "provider unavailable",
          headers: {
            "x-model-routed": "image-basic",
            "x-provider-used": "openai",
            "x-provider-model": "gpt-image-1",
          },
        })),
      },
    } as never);

    const handler = app.handlers.get("POST /v1/images/generations");
    let statusCode = 200;
    const headers = new Map<string, string>();
    const reply = {
      code: (code: number) => {
        statusCode = code;
        return {
          send: (body: unknown) => body,
        };
      },
      send: (body: unknown) => body,
      header: (key: string, value: string) => {
        headers.set(key, value);
        return reply;
      },
    };
    const response = (await handler?.(
      {
        headers: { "x-api-key": "sk_1" },
        body: {
          model: "image-basic",
          prompt: "a lighthouse in fog",
          response_format: "url",
        },
      },
      reply,
    )) as { error: string };

    expect(statusCode).toBe(502);
    expect(headers.get("x-model-routed")).toBe("image-basic");
    expect(response).toEqual({ error: "provider unavailable" });
    expect(headers.get("x-provider-used")).toBe("openai");
    expect(headers.get("x-provider-model")).toBe("gpt-image-1");
    expect(JSON.stringify(response)).not.toContain("internal");
  });

  it("rejects empty prompts before calling the runtime service", async () => {
    const imageGeneration = vi.fn();
    const app = new FakeApp();

    registerImagesGenerationsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["image"] }) },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: true, twoFactorEnabled: false }),
        canUse: vi.fn((key: string, settings: Record<string, boolean>) => settings[key]),
      },
      rateLimiter: { allow: async () => true },
      ai: { imageGeneration },
    } as never);

    const handler = app.handlers.get("POST /v1/images/generations");
    let statusCode = 200;
    const reply = {
      code: (code: number) => {
        statusCode = code;
        return {
          send: (body: unknown) => body,
        };
      },
      send: (body: unknown) => body,
      header: () => reply,
    };

    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" }, body: { prompt: "   " } },
      reply,
    )) as { error: string };

    expect(statusCode).toBe(400);
    expect(response).toEqual({ error: "prompt is required" });
    expect(imageGeneration).not.toHaveBeenCalled();
  });
});
