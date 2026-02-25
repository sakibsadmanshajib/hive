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

  it("patch /v1/users/settings writes through supabase repository", async () => {
    vi.resetModules();

    const upsertSettings = vi.fn(async () => undefined);

    vi.doMock("../../src/config/env", () => ({
      getEnv: () => ({
        nodeEnv: "test",
        port: 8080,
        postgresUrl: "postgres://test",
        redisUrl: "redis://127.0.0.1:6379",
        rateLimitPerMinute: 60,
        adminStatusToken: "admin",
        allowDemoPaymentConfirm: true,
        allowDevApiKeyPrefix: false,
        google: { clientId: "id", clientSecret: "secret", redirectUri: "http://127.0.0.1/callback" },
        auth: {
          sessionTtlMinutes: 60,
          enforceTwoFactorSensitiveActions: false,
          twoFactorVerificationWindowMinutes: 10,
        },
        webhookSecrets: { bkash: "bk", sslcommerz: "ssl" },
        bkash: {},
        sslcommerz: {},
        supabase: {
          url: "https://demo.supabase.co",
          serviceRoleKey: "service-role-key",
          flags: {
            authEnabled: false,
            userRepoEnabled: true,
            apiKeysEnabled: false,
            billingStoreEnabled: false,
          },
        },
        providers: {
          circuitBreaker: { failureThreshold: 5, resetTimeoutMs: 30000 },
          ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 4000, maxRetries: 1 },
          groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 4000, maxRetries: 1 },
          openrouter: { baseUrl: "https://openrouter.ai/api/v1", model: "meta-llama/llama-3.1-8b-instruct", timeoutMs: 4000, maxRetries: 1 },
        },
        langfuse: {
          enabled: false,
          baseUrl: "https://cloud.langfuse.com",
          publicKey: undefined,
          secretKey: undefined,
        },
      }),
    }));

    vi.doMock("../../src/runtime/supabase-user-store", () => ({
      SupabaseUserStore: class {
        async findById() {
          return undefined;
        }
        async findByEmail() {
          return undefined;
        }
        upsertSettings = upsertSettings;
        async getSettings() {
          return { apiEnabled: true, generateImage: true, twoFactorEnabled: false };
        }
      },
    }));

    vi.doMock("../../src/runtime/postgres-store", () => ({
      PostgresStore: class {
        async upsertUserSetting() {
          return undefined;
        }
        async getUserSettings() {
          return {};
        }
      },
    }));

    const { createRuntimeServices } = await import("../../src/runtime/services");
    const services = createRuntimeServices();

    await services.userSettings.updateForUser("8f57d0e6-2ecb-4e8d-a774-5f4eeaf9a5ec", { generateImage: false });

    expect(upsertSettings).toHaveBeenCalledWith("8f57d0e6-2ecb-4e8d-a774-5f4eeaf9a5ec", { generateImage: false });
  });
});
