import { describe, expect, it } from "vitest";
import { registerUsageRoute } from "../../src/routes/usage";

type RegisteredHandler = (request?: { headers?: Record<string, string> }, reply?: {
  code: (statusCode: number) => { send: (payload: unknown) => unknown };
  send: (payload: unknown) => unknown;
}) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, RegisteredHandler>();

  get(path: string, handler: RegisteredHandler) {
    this.handlers.set(path, handler);
  }
}

describe("usage route", () => {
  it("returns raw events plus the aggregated summary", async () => {
    const app = new FakeApp();
    registerUsageRoute(app as never, {
      env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: false } } },
      users: {
        resolveApiKey: async () => ({ userId: "user-1", scopes: ["usage"] }),
      },
      authz: { requirePermission: async () => true },
      userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      usage: {
        listWithSummary: async () => ({
          summary: {
            windowDays: 7,
            totalRequests: 2,
            totalCredits: 25,
            daily: [
              { date: "2026-03-12", requests: 1, credits: 10 },
              { date: "2026-03-13", requests: 1, credits: 15 },
            ],
            byModel: [{ key: "fast-chat", requests: 2, credits: 25 }],
            byEndpoint: [{ key: "/v1/chat/completions", requests: 2, credits: 25 }],
            byChannel: [{ key: "web", requests: 2, credits: 25 }],
            byApiKey: [{ key: "key-123", requests: 1, credits: 10 }],
          },
          data: [
            {
              id: "usage_1",
              userId: "user-1",
              endpoint: "/v1/chat/completions",
              model: "fast-chat",
              credits: 15,
              channel: "web",
              apiKeyId: "key-123",
              createdAt: "2026-03-13T10:00:00.000Z",
            },
          ],
        }),
      },
      supabaseAuth: { getSessionPrincipal: async () => null },
    } as never);

    const handler = app.handlers.get("/v1/usage");
    const payload = await handler?.(
      { headers: { "x-api-key": "sk_test" } },
      {
        code: () => ({ send: (body: unknown) => body }),
        send: (body: unknown) => body,
      },
    ) as {
      object: string;
      summary: { totalCredits: number; byChannel: Array<{ key: string }>; byApiKey: Array<{ key: string }> };
      data: Array<{ id: string; channel: string; apiKeyId?: string }>;
    };

    expect(payload.object).toBe("list");
    expect(payload.summary.totalCredits).toBe(25);
    expect(payload.summary.byChannel[0]?.key).toBe("web");
    expect(payload.summary.byApiKey[0]?.key).toBe("key-123");
    expect(payload.data[0]?.id).toBe("usage_1");
    expect(payload.data[0]?.channel).toBe("web");
    expect(payload.data[0]?.apiKeyId).toBe("key-123");
  });
});
