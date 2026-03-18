import { describe, expect, it, vi } from "vitest";
import { registerImagesGenerationsRoute } from "../../src/routes/images-generations";
import { registerProvidersMetricsRoute } from "../../src/routes/providers-metrics";
import { registerProvidersStatusRoute } from "../../src/routes/providers-status";

type Handler = (request?: { headers?: Record<string, string>; body?: unknown }, reply?: { code: (status: number) => unknown; send: (payload: unknown) => unknown; header: (key: string, value: string) => unknown }) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, Handler>();

  post(path: string, optsOrHandler: Handler | Record<string, unknown>, handler?: Handler) {
    this.handlers.set(`POST ${path}`, handler ?? (optsOrHandler as Handler));
  }

  get(path: string, handler: Handler) {
    this.handlers.set(`GET ${path}`, handler);
  }
}

describe("rbac + settings enforcement", () => {
  it("returns 403 when permission exists but setting disabled", async () => {
    const app = new FakeApp();
    registerImagesGenerationsRoute(app as never, {
      env: { allowDevApiKeyPrefix: false },
      auth: { getSessionPrincipal: async () => null },
      users: { resolveApiKey: async () => ({ userId: "user_1", scopes: ["image"] }) },
      authz: { requirePermission: async () => true },
      userSettings: {
        getForUser: async () => ({ apiEnabled: true, generateImage: false, twoFactorEnabled: false }),
        canUse: vi.fn((key: string, settings: Record<string, boolean>) => settings[key]),
      },
      rateLimiter: { allow: async () => true },
      ai: { imageGeneration: vi.fn() },
    } as never);

    const handler = app.handlers.get("POST /v1/images/generations");
    let statusCode = 200;
    let sentPayload: unknown;
    const response = (await handler?.(
      { headers: { "x-api-key": "sk_1" }, body: { prompt: "a cat" } },
      {
        code: (code: number) => {
          statusCode = code;
          return { send: (payload: unknown) => { sentPayload = payload; return payload; } };
        },
        send: (payload: unknown) => { sentPayload = payload; return payload; },
        header: () => undefined,
      },
    )) as { error: string } | undefined;

    expect(statusCode).toBe(403);
    const payload = (response ?? sentPayload) as { error: { message: string } };
    expect(payload.error.message).toContain("setting disabled");
  });

  it("keeps provider status internal endpoint token-protected", async () => {
    const app = new FakeApp();
    registerProvidersStatusRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      ai: {
        providersStatus: async () => ({ providers: [{ name: "mock", enabled: true, healthy: true, detail: "ok" }] }),
      },
    } as never);

    const internal = app.handlers.get("GET /v1/providers/status/internal");
    const unauthorized = (await internal?.(
      { headers: {} },
      { code: () => ({ send: (payload: unknown) => payload }), send: (payload: unknown) => payload, header: () => undefined },
    )) as { error: string };

    expect(unauthorized.error).toBe("unauthorized");
  });

  it("keeps provider metrics internal endpoints token-protected", async () => {
    const app = new FakeApp();
    const providersMetrics = vi.fn(async () => ({
      scrapedAt: "2026-03-11T00:00:00.000Z",
      providers: [
        {
          name: "mock",
          enabled: true,
          healthy: true,
          detail: "ok",
          circuit: { state: "CLOSED", failures: 0 },
          requests: 1,
          errors: 0,
          errorRate: 0,
          latencyMs: { avg: 10, p95: 10 },
        },
      ],
    }));
    const providersMetricsPrometheus = vi.fn(async () => ({
      contentType: "text/plain; version=0.0.4; charset=utf-8",
      body: "hive_provider_requests_total{provider=\"mock\"} 1",
    }));
    registerProvidersMetricsRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      ai: {
        providersMetrics,
        providersMetricsPrometheus,
      },
    } as never);

    const internal = app.handlers.get("GET /v1/providers/metrics/internal");
    const prometheus = app.handlers.get("GET /v1/providers/metrics/internal/prometheus");
    const statusCodes: number[] = [];

    const unauthorizedJson = (await internal?.(
      { headers: {} },
      {
        code: (statusCode: number) => {
          statusCodes.push(statusCode);
          return { send: (payload: unknown) => payload };
        },
        send: (payload: unknown) => payload,
        header: () => undefined,
      },
    )) as { error: string };
    const unauthorizedPrometheus = (await prometheus?.(
      { headers: {} },
      {
        code: (statusCode: number) => {
          statusCodes.push(statusCode);
          return { send: (payload: unknown) => payload };
        },
        send: (payload: unknown) => payload,
        header: () => undefined,
      },
    )) as { error: string };

    expect(statusCodes).toEqual([401, 401]);
    expect(unauthorizedJson.error).toBe("unauthorized");
    expect(unauthorizedPrometheus.error).toBe("unauthorized");
    expect(providersMetrics).not.toHaveBeenCalled();
    expect(providersMetricsPrometheus).not.toHaveBeenCalled();
  });
});
