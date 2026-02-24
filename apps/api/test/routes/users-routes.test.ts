import { describe, expect, it, vi } from "vitest";
import { registerUserRoutes } from "../../src/routes/users";

type Handler = (
  request?: { body?: unknown; headers?: Record<string, string> },
  reply?: { code: (status: number) => unknown; send?: (payload: unknown) => unknown },
) => Promise<unknown>;

function fakeReply() {
  return {
    code: () => ({
      send: (payload: unknown) => payload,
    }),
    send: (payload: unknown) => payload,
  };
}

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

describe("user routes", () => {
  it("register returns api key payload", async () => {
    const app = new FakeApp();
    registerUserRoutes(app as never, {
      users: {
        register: vi.fn(async () => ({ userId: "user_1", email: "a@b.com", name: "A", apiKey: "sk_live_123" })),
      },
    } as never);

    const handler = app.handlers.get("POST /v1/users/register");
    const response = (await handler?.({ body: { email: "a@b.com", password: "password123", name: "A" } }, fakeReply())) as {
      api_key: string;
    };

    expect(response.api_key).toBe("sk_live_123");
  });

  it("returns /v1/users/me from supabase store when user repo flag enabled", async () => {
    vi.resetModules();

    const supabaseFindById = vi.fn(async () => ({
      userId: "8f57d0e6-2ecb-4e8d-a774-5f4eeaf9a5ec",
      email: "supabase@example.com",
      name: "Supabase User",
      createdAt: "2026-02-23T10:00:00.000Z",
    }));

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
            authEnabled: true,
            userRepoEnabled: true,
            apiKeysEnabled: false,
            billingStoreEnabled: false,
          },
        },
        providers: {
          ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b" },
          groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant" },
        },
        langfuse: {
          enabled: false,
          baseUrl: "https://cloud.langfuse.com",
          publicKey: undefined,
          secretKey: undefined,
        },
      }),
    }));

    vi.doMock("../../src/runtime/supabase-auth-service", () => ({
      SupabaseAuthService: class {
        async getSessionPrincipal() {
          return { userId: "8f57d0e6-2ecb-4e8d-a774-5f4eeaf9a5ec" };
        }
      },
    }));

    vi.doMock("../../src/runtime/supabase-user-store", () => ({
      SupabaseUserStore: class {
        findById = supabaseFindById;
        async findByEmail() {
          return undefined;
        }
        async upsertSettings() {
          return undefined;
        }
        async getSettings() {
          return {};
        }
      },
    }));

    vi.doMock("../../src/runtime/postgres-store", () => ({
      PostgresStore: class {
        async getBalance() {
          return { available: 120, purchased: 100, promo: 20 };
        }
        async listApiKeys() {
          return [];
        }
        async getApiKey() {
          return undefined;
        }
        async listPermissionsForUser() {
          return [];
        }
      },
    }));

    const [{ createRuntimeServices }, { registerUserRoutes: registerRoutes }] = await Promise.all([
      import("../../src/runtime/services"),
      import("../../src/routes/users"),
    ]);

    const services = createRuntimeServices();
    const app = new FakeApp();
    registerRoutes(app as never, services as never);

    const handler = app.handlers.get("GET /v1/users/me");
    const response = (await handler?.(
      { headers: { authorization: "Bearer sb_session_token" } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { user: { user_id: string; email: string } };

    expect(supabaseFindById).toHaveBeenCalledWith("8f57d0e6-2ecb-4e8d-a774-5f4eeaf9a5ec");
    expect(response.user.user_id).toBe("8f57d0e6-2ecb-4e8d-a774-5f4eeaf9a5ec");
    expect(response.user.email).toBe("supabase@example.com");
  });
});
