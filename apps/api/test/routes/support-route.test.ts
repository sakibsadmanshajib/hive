import { describe, expect, it } from "vitest";
import { registerSupportRoute } from "../../src/routes/support";

type RegisteredHandler = (
  request?: { headers?: Record<string, string>; params?: Record<string, string> },
  reply?: { code: (statusCode: number) => unknown },
) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, RegisteredHandler>();

  get(path: string, handler: RegisteredHandler) {
    this.handlers.set(path, handler);
  }
}

describe("support route", () => {
  it("requires an admin token for the support snapshot", async () => {
    const app = new FakeApp();
    registerSupportRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
    } as never);

    const handler = app.handlers.get("/v1/support/users/:userId");
    const statusCodes: number[] = [];
    const payload = await handler?.(
      { headers: {}, params: { userId: "user-1" } },
      { code: (statusCode) => statusCodes.push(statusCode) },
    ) as { error: string };

    expect(statusCodes).toEqual([401]);
    expect(payload).toEqual({ error: "unauthorized" });
  });

  it("returns a single-user troubleshooting snapshot for operators", async () => {
    const app = new FakeApp();
    registerSupportRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      users: {
        me: async () => ({
          userId: "user-1",
          email: "user@example.com",
          name: "Test User",
          createdAt: "2026-01-01T00:00:00.000Z",
          apiKeys: [{ id: "key-1", key_id: "sk_live_abcd", nickname: "default", status: "active", revoked: false, scopes: ["chat"], createdAt: "2026-03-01T00:00:00.000Z" }],
          apiKeyEvents: [{ id: "evt-1", apiKeyId: "key-1", userId: "user-1", eventType: "created", eventAt: "2026-03-01T00:00:00.000Z", metadata: {} }],
        }),
      },
      credits: {
        getBalance: async () => ({
          userId: "user-1",
          availableCredits: 120,
          purchasedCredits: 100,
          promoCredits: 20,
        }),
      },
      usage: {
        listWithSummary: async () => ({
          summary: {
            windowDays: 7,
            totalRequests: 2,
            totalCredits: 25,
            daily: [],
            byModel: [{ key: "fast-chat", requests: 2, credits: 25 }],
            byEndpoint: [{ key: "/v1/chat/completions", requests: 2, credits: 25 }],
          },
          data: [
            {
              id: "usage_1",
              userId: "user-1",
              endpoint: "/v1/chat/completions",
              model: "fast-chat",
              credits: 25,
              createdAt: "2026-03-13T10:00:00.000Z",
            },
          ],
        }),
      },
    } as never);

    const handler = app.handlers.get("/v1/support/users/:userId");
    const payload = await handler?.(
      { headers: { "x-admin-token": "admin-token" }, params: { userId: "user-1" } },
      { code: () => undefined },
    ) as { object: string; data: { user: { userId: string }; usage: { summary: { totalCredits: number } } } };

    expect(payload.object).toBe("support.user");
    expect(payload.data.user.userId).toBe("user-1");
    expect(payload.data.usage.summary.totalCredits).toBe(25);
  });

  it("returns 404 when the requested support user does not exist", async () => {
    const app = new FakeApp();
    registerSupportRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      users: {
        me: async () => undefined,
      },
    } as never);

    const handler = app.handlers.get("/v1/support/users/:userId");
    const statusCodes: number[] = [];
    const payload = await handler?.(
      { headers: { "x-admin-token": "admin-token" }, params: { userId: "missing-user" } },
      { code: (statusCode) => statusCodes.push(statusCode) },
    ) as { error: string };

    expect(statusCodes).toEqual([404]);
    expect(payload).toEqual({ error: "user not found" });
  });
});
