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

describe("two-factor enforcement", () => {
  it("blocks api-key creation when 2fa is required but not recently verified", async () => {
    const app = new FakeApp();
    registerUserRoutes(app as never, {
      env: {
        allowDevApiKeyPrefix: false,
        auth: { enforceTwoFactorSensitiveActions: true, twoFactorVerificationWindowMinutes: 10 },
      },
      auth: { getSessionPrincipal: async () => null },
      users: {
        resolveApiKey: async () => ({ userId: "user_1", scopes: ["usage", "billing"] }),
        createApiKey: vi.fn(),
      },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: true, twoFactorEnabled: true }),
        canUse: (key: string, settings: Record<string, boolean>) => settings[key],
      },
      twoFactor: {
        hasRecentVerification: vi.fn(async () => false),
      },
    } as never);

    const handler = app.handlers.get("POST /v1/users/api-keys");
    let payload: unknown;
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" }, body: { scopes: ["chat"] } },
      {
        code: () => ({ send: (sent: unknown) => { payload = sent; return sent; } }),
        send: (sent: unknown) => { payload = sent; return sent; },
      },
    )) as { error: string } | undefined;

    const sent = (response ?? payload) as { error: string };
    expect(sent.error).toBe("two-factor verification required");
  });
});
