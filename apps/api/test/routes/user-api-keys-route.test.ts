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

describe("user api-key management route registration", () => {
  it("registers the authenticated user and api-key management endpoints", () => {
    const app = new FakeApp();

    registerRoutes(app as never, {} as never);

    expect(app.handlers.has("GET /v1/users/me")).toBe(true);
    expect(app.handlers.has("GET /v1/users/api-keys")).toBe(true);
    expect(app.handlers.has("POST /v1/users/api-keys")).toBe(true);
    expect(app.handlers.has("POST /v1/users/api-keys/:id/revoke")).toBe(true);
    expect(app.handlers.has("GET /v1/support/users/:userId")).toBe(true);
  });

  it("creates and revokes api keys through the authenticated user routes", async () => {
    const app = new FakeApp();
    const futureExpiry = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
    registerRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
      supabaseAuth: { getSessionPrincipal: async () => ({ userId: "user-1" }) },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      credits: {
        getBalance: async () => ({
          userId: "user-1",
          availableCredits: 250,
          purchasedCredits: 200,
          promoCredits: 50,
        }),
      },
      users: {
        me: async () => ({
          userId: "user-1",
          email: "test@example.com",
          createdAt: "2026-01-01T00:00:00.000Z",
          apiKeys: [],
          apiKeyEvents: [],
        }),
        createApiKey: async (_userId: string, input: Record<string, unknown>) => ({
          key: "sk_live_created",
          ...input,
        }),
        revokeApiKey: async () => true,
        resolveApiKey: async () => null,
      },
    } as never);

    const create = app.handlers.get("POST /v1/users/api-keys");
    const revoke = app.handlers.get("POST /v1/users/api-keys/:id/revoke");
    const me = app.handlers.get("GET /v1/users/me");
    let statusCode = 200;
    const reply = {
      code: (status: number) => {
        statusCode = status;
        return {
          send: (payload: unknown) => payload,
        };
      },
      send: (payload: unknown) => payload,
    };

    const created = await create?.(
      {
        headers: { authorization: "Bearer token" },
        body: { nickname: "deploy", scopes: ["chat"], expiresAt: futureExpiry },
      },
      reply,
    ) as Record<string, unknown>;

    const mePayload = await me?.(
      {
        headers: { authorization: "Bearer token" },
      },
      reply,
    ) as Record<string, unknown>;

    expect(statusCode).toBe(201);
    expect(mePayload).toMatchObject({
      credits: {
        availableCredits: 250,
        purchasedCredits: 200,
        promoCredits: 50,
      },
    });
    expect(created).toMatchObject({
      key: "sk_live_created",
      nickname: "deploy",
      scopes: ["chat"],
      expiresAt: futureExpiry,
    });

    const revoked = await revoke?.(
      {
        headers: { authorization: "Bearer token" },
        params: { id: "key-1" },
      },
      reply,
    ) as Record<string, unknown>;

    expect(revoked).toEqual({ revoked: true, id: "key-1" });
  });

  it("rejects invalid expiresAt values for api key creation", async () => {
    const app = new FakeApp();
    registerRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
      supabaseAuth: { getSessionPrincipal: async () => ({ userId: "user-1" }) },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      users: {
        createApiKey: async () => ({ key: "sk_live_created" }),
        resolveApiKey: async () => null,
      },
    } as never);

    const create = app.handlers.get("POST /v1/users/api-keys");
    let statusCode = 200;
    let sentPayload: unknown;
    await create?.(
      {
        headers: { authorization: "Bearer token" },
        body: { nickname: "deploy", scopes: ["chat"], expiresAt: "not-a-date" },
      },
      {
        code: (status: number) => {
          statusCode = status;
          return {
            send: (body: unknown) => {
              sentPayload = body;
              return body;
            },
          };
        },
        send: (body: unknown) => {
          sentPayload = body;
          return body;
        },
      },
    );

    expect(statusCode).toBe(400);
    expect(sentPayload).toEqual({ error: "invalid api key create request" });
  });
});
