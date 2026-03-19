import { describe, expect, it, vi } from "vitest";
import { registerRoutes } from "../../src/routes";

class FakeApp {
  readonly handlers = new Map<string, (request?: any, reply?: any) => Promise<unknown>>();

  get(path: string, handler: (request?: any, reply?: any) => Promise<unknown>) {
    this.handlers.set(`GET ${path}`, handler);
  }

  post(path: string, handler: (request?: any, reply?: any) => Promise<unknown>) {
    this.handlers.set(`POST ${path}`, handler);
  }

  register(plugin: (app: any, opts: any) => Promise<void>, opts: any) {
    return plugin(this, opts);
  }

  delete(..._args: any[]) {}
  addHook() {}
  setErrorHandler() {}
  setNotFoundHandler() {}
}

function createReply() {
  let statusCode = 200;
  let sentPayload: unknown;

  return {
    get statusCode() {
      return statusCode;
    },
    get sentPayload() {
      return sentPayload;
    },
    code(status: number) {
      statusCode = status;
      return this;
    },
    send(payload: unknown) {
      sentPayload = payload;
      return payload;
    },
    header() {
      return this;
    },
  };
}

describe("guest attribution routes", () => {
  it("registers internal guest attribution endpoints", () => {
    const app = new FakeApp();

    registerRoutes(app as never, {} as never);

    expect(app.handlers.has("POST /v1/internal/guest/session")).toBe(true);
    expect(app.handlers.has("POST /v1/internal/guest/link")).toBe(true);
  });

  it("persists guest sessions behind the trusted web token", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    const upsertSession = vi.fn(async () => undefined);
    const app = new FakeApp();
    registerRoutes(app as never, {
      guests: { upsertSession },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/guest/session");
    const reply = createReply();

    await handler?.(
      {
        headers: {
          "x-web-guest-token": "test-web-token",
        },
        body: {
          guestId: "guest_123",
          expiresAt: "2026-03-20T00:00:00.000Z",
          lastSeenIp: "203.0.113.10",
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(201);
    expect(upsertSession).toHaveBeenCalledWith({
      guestId: "guest_123",
      expiresAt: "2026-03-20T00:00:00.000Z",
      lastSeenIp: "203.0.113.10",
    });
  });

  it("links guest identities to authenticated users", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    const linkGuest = vi.fn(async () => undefined);
    const claimGuestSessionsForUser = vi.fn(async () => undefined);
    const app = new FakeApp();
    registerRoutes(app as never, {
      env: {
        supabase: {
          flags: {
            authEnabled: false,
          },
        },
        allowDevApiKeyPrefix: false,
      },
      users: {
        resolveApiKey: async () => ({ userId: "user_123", scopes: ["chat"] }),
        linkGuest,
      },
      chatHistory: { claimGuestSessionsForUser },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/guest/link");
    const reply = createReply();

    const result = await handler?.(
      {
        headers: {
          authorization: "Bearer api_key_like_token",
          "x-web-guest-token": "test-web-token",
          "x-guest-id": "guest_123",
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(200);
    expect(linkGuest).toHaveBeenCalledWith("guest_123", "user_123", "auth_session");
    expect(claimGuestSessionsForUser).toHaveBeenCalledWith("guest_123", "user_123");
    expect(result).toEqual({
      guestId: "guest_123",
      linked: true,
      userId: "user_123",
    });
  });

  it("ensures the session-authenticated user exists locally before linking a guest", async () => {
    process.env.WEB_INTERNAL_GUEST_TOKEN = "test-web-token";
    const ensureSessionUser = vi.fn(async () => undefined);
    const linkGuest = vi.fn(async () => undefined);
    const app = new FakeApp();
    registerRoutes(app as never, {
      env: {
        supabase: {
          flags: {
            authEnabled: true,
          },
        },
        allowDevApiKeyPrefix: false,
      },
      supabaseAuth: {
        getSessionPrincipal: async () => ({
          userId: "user_session",
          email: "demo@example.com",
          name: "Demo User",
        }),
      },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      users: {
        ensureSessionUser,
        resolveApiKey: async () => null,
        linkGuest,
      },
      chatHistory: { claimGuestSessionsForUser: vi.fn(async () => undefined) },
    } as never);

    const handler = app.handlers.get("POST /v1/internal/guest/link");
    const reply = createReply();

    await handler?.(
      {
        headers: {
          authorization: "Bearer session-token",
          "x-web-guest-token": "test-web-token",
          "x-guest-id": "guest_123",
        },
      },
      reply,
    );

    expect(reply.statusCode).toBe(200);
    expect(ensureSessionUser).toHaveBeenCalledWith({
      userId: "user_session",
      email: "demo@example.com",
      name: "Demo User",
    });
    expect(ensureSessionUser.mock.invocationCallOrder[0]).toBeLessThan(linkGuest.mock.invocationCallOrder[0]);
  });
});
