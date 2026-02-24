import { describe, expect, it, vi } from "vitest";
import { registerGoogleAuthRoutes } from "../../src/routes/google-auth";

type Handler = (request?: { query?: Record<string, string>; headers?: Record<string, string> }, reply?: { code: (status: number) => unknown; send: (payload: unknown) => unknown }) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, Handler>();

  get(path: string, handler: Handler) {
    this.handlers.set(`GET ${path}`, handler);
  }

  post(path: string, handler: Handler) {
    this.handlers.set(`POST ${path}`, handler);
  }
}

describe("google auth routes", () => {
  it("start endpoint returns url with state", async () => {
    const app = new FakeApp();
    registerGoogleAuthRoutes(app as never, {
      auth: {
        startGoogleAuth: vi.fn(async () => ({
          state: "state_1",
          authorizationUrl: "https://accounts.google.com/o/oauth2/v2/auth?state=state_1",
        })),
      },
    } as never);

    const handler = app.handlers.get("GET /v1/auth/google/start");
    const response = (await handler?.()) as { state: string; authorization_url: string };

    expect(response.state).toBe("state_1");
    expect(response.authorization_url).toContain("state=state_1");
  });

  it("callback rejects invalid state", async () => {
    const app = new FakeApp();
    registerGoogleAuthRoutes(app as never, {
      auth: {
        completeGoogleAuth: vi.fn(async () => ({ error: "invalid oauth state" })),
      },
    } as never);

    const handler = app.handlers.get("GET /v1/auth/google/callback");
    const response = (await handler?.(
      { query: { state: "bad", code: "x" } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { error: string };

    expect(response.error).toBe("invalid oauth state");
  });

  it("callback succeeds with verifier-backed service", async () => {
    const app = new FakeApp();
    registerGoogleAuthRoutes(app as never, {
      auth: {
        completeGoogleAuth: vi.fn(async () => ({
          sessionToken: "sess_1",
          userId: "user_1",
          email: "u@example.com",
          name: "User",
        })),
      },
    } as never);

    const handler = app.handlers.get("GET /v1/auth/google/callback");
    const response = (await handler?.(
      { query: { state: "ok", code: "ok" } },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload },
    )) as { session_token: string };

    expect(response.session_token).toBe("sess_1");
  });
});
