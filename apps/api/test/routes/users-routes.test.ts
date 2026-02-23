import { describe, expect, it, vi } from "vitest";
import { registerUserRoutes } from "../../src/routes/users";

type Handler = (request?: { body?: unknown; headers?: Record<string, string> }, reply?: { code: (status: number) => unknown }) => Promise<unknown>;

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
});
