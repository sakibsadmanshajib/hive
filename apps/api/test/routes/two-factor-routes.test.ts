import { describe, expect, it, vi } from "vitest";
import { registerTwoFactorRoutes } from "../../src/routes/two-factor";

type Handler = (request?: { body?: unknown; headers?: Record<string, string> }, reply?: { code: (status: number) => unknown; send: (payload: unknown) => unknown }) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, Handler>();

  post(path: string, handler: Handler) {
    this.handlers.set(path, handler);
  }
}

describe("two-factor routes", () => {
  it("enroll init returns challenge metadata", async () => {
    const app = new FakeApp();
    registerTwoFactorRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["usage"] }) },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      twoFactor: {
        initEnrollment: vi.fn(async () => ({ challengeId: "chlg_1", secret: "secret_1", method: "totp" })),
      },
    } as never);

    const handler = app.handlers.get("/v1/2fa/enroll/init");
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { challenge_id: string };

    expect(response.challenge_id).toBe("chlg_1");
  });

  it("enroll verify enables twoFactorEnabled", async () => {
    const app = new FakeApp();
    const updateForUser = vi.fn(async () => ({ twoFactorEnabled: true }));
    registerTwoFactorRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["usage"] }) },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true, updateForUser },
      twoFactor: {
        verifyEnrollment: vi.fn(async () => true),
      },
    } as never);

    const handler = app.handlers.get("/v1/2fa/enroll/verify");
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" }, body: { challenge_id: "chlg_1", code: "000000" } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { two_factor_enabled: boolean };

    expect(response.two_factor_enabled).toBe(true);
    expect(updateForUser).toHaveBeenCalledWith("user_1", { twoFactorEnabled: true });
  });

  it("challenge verify returns success/failure", async () => {
    const app = new FakeApp();
    registerTwoFactorRoutes(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["usage"] }) },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      twoFactor: {
        verifyChallenge: vi.fn(async () => false),
      },
    } as never);

    const handler = app.handlers.get("/v1/2fa/challenge/verify");
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" }, body: { challenge_id: "chlg_1", code: "999999" } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { success: boolean };

    expect(response.success).toBe(false);
  });
});
