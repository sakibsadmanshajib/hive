import { describe, expect, it, vi } from "vitest";
import { registerUserRoutes } from "../../src/routes/users";

type Handler = (request?: { body?: unknown; headers?: Record<string, string> }, reply?: { code: (status: number) => unknown; send: (payload: unknown) => unknown }) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, Handler>();

  post(path: string, handler: Handler) {
    this.handlers.set(`POST ${path}`, handler);
  }

  get(path: string, handler: Handler) {
    this.handlers.set(`GET ${path}`, handler);
  }

  delete(path: string, handler: Handler) {
    this.handlers.set(`DELETE ${path}`, handler);
  }

  patch(path: string, handler: Handler) {
    this.handlers.set(`PATCH ${path}`, handler);
  }
}

describe("user settings routes", () => {
  it("GET /v1/users/settings returns settings payload", async () => {
    const app = new FakeApp();
    registerUserRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false, auth: { enforceTwoFactorSensitiveActions: false, twoFactorVerificationWindowMinutes: 10 } },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["usage"] }) },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: false, twoFactorEnabled: true }),
        canUse: () => true,
        updateForUser: vi.fn(),
      },
    } as never);

    const handler = app.handlers.get("GET /v1/users/settings");
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { settings: { twoFactorEnabled: boolean } };

    expect(response.settings.twoFactorEnabled).toBe(true);
  });

  it("PATCH /v1/users/settings validates allowed keys", async () => {
    const app = new FakeApp();
    registerUserRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false, auth: { enforceTwoFactorSensitiveActions: false, twoFactorVerificationWindowMinutes: 10 } },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["usage"] }) },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: true, twoFactorEnabled: false }),
        canUse: () => true,
        updateForUser: vi.fn(async () => ({ apiEnabled: true, generateImage: true, twoFactorEnabled: false })),
      },
    } as never);

    const handler = app.handlers.get("PATCH /v1/users/settings") ?? app.handlers.get("PATCH /v1/users/settings");
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" }, body: { unknownFlag: true } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { error: string };

    expect(response.error).toBe("invalid setting keys");
  });

  it("apiEnabled=false blocks api-key creation flow", async () => {
    const app = new FakeApp();
    registerUserRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false, auth: { enforceTwoFactorSensitiveActions: false, twoFactorVerificationWindowMinutes: 10 } },
      auth: { getSessionPrincipal: async () => null },
      users: {
        resolveApiKey: async () => ({ userId: "user_1", scopes: ["usage", "billing"] }),
        createApiKey: vi.fn(),
      },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: false, generateImage: true, twoFactorEnabled: false }),
        canUse: (key: string, settings: Record<string, boolean>) => settings[key],
      },
      twoFactor: { hasRecentVerification: async () => true },
    } as never);

    const handler = app.handlers.get("POST /v1/users/api-keys") ?? app.handlers.get("POST /v1/users/api-keys");
    let payload: unknown;
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" }, body: {} },
      {
        code: () => ({ send: (sent: unknown) => { payload = sent; return sent; } }),
        send: (sent: unknown) => { payload = sent; return sent; },
      },
    )) as { error: string } | undefined;

    const sent = (response ?? payload) as { error: string };
    expect(sent.error).toContain("setting disabled");
  });
});
