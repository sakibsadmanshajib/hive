import { describe, expect, it } from "vitest";
import { registerProvidersMetricsRoute } from "../../src/routes/providers-metrics";

type RegisteredHandler = (
  request?: { headers?: Record<string, string> },
  reply?: { code: (statusCode: number) => unknown; header: (key: string, value: string) => unknown },
) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, RegisteredHandler>();

  get(path: string, handler: RegisteredHandler) {
    this.handlers.set(path, handler);
  }
}

describe("providers metrics routes", () => {
  it("returns sanitized public metrics without internal detail", async () => {
    const app = new FakeApp();
    registerProvidersMetricsRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      ai: {
        providersMetrics: async () => ({
          scrapedAt: "2026-03-11T00:00:00.000Z",
          providers: [
            {
              name: "ollama",
              enabled: true,
              healthy: true,
              detail: "reachable",
              circuit: { state: "CLOSED", failures: 0 },
              requests: 3,
              errors: 1,
              errorRate: 0.3333,
              latencyMs: { avg: 120, p95: 180 },
            },
          ],
        }),
      },
    } as never);

    const handler = app.handlers.get("/v1/providers/metrics");
    const payload = (await handler?.(
      { headers: {} },
      { code: () => undefined, header: () => undefined },
    )) as { scrapedAt: string; data: Array<Record<string, string | number | boolean | object>> };

    expect(payload.scrapedAt).toBe("2026-03-11T00:00:00.000Z");
    expect(payload.data[0]).toEqual({
      name: "ollama",
      enabled: true,
      healthy: true,
      circuitState: "closed",
      requests: 3,
      errors: 1,
      errorRate: 0.3333,
      latencyMs: { avg: 120, p95: 180 },
    });
    expect("detail" in payload.data[0]).toBe(false);
    expect("circuit" in payload.data[0]).toBe(false);
  });

  it("requires admin token for internal metrics routes and exposes diagnostics when authorized", async () => {
    const app = new FakeApp();
    const headers: Record<string, string> = {};

    registerProvidersMetricsRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      ai: {
        providersMetrics: async () => ({
          scrapedAt: "2026-03-11T00:00:00.000Z",
          providers: [
            {
              name: "mock",
              enabled: true,
              healthy: false,
              detail: "timeout",
              circuit: { state: "OPEN", failures: 5, lastError: "timeout" },
              requests: 10,
              errors: 5,
              errorRate: 0.5,
              latencyMs: { avg: 220, p95: 400 },
            },
          ],
        }),
        providersMetricsPrometheus: async () => ({
          contentType: "text/plain; version=0.0.4; charset=utf-8",
          body: "hive_provider_requests_total{provider=\"mock\"} 10",
        }),
      },
    } as never);

    const internal = app.handlers.get("/v1/providers/metrics/internal");
    const prometheus = app.handlers.get("/v1/providers/metrics/internal/prometheus");
    const statusCodes: number[] = [];

    const unauthorized = (await internal?.(
      { headers: {} },
      { code: (statusCode) => statusCodes.push(statusCode), header: () => undefined },
    )) as { error: string };
    const unauthorizedPrometheus = (await prometheus?.(
      { headers: {} },
      { code: (statusCode) => statusCodes.push(statusCode), header: () => undefined },
    )) as { error: string };
    const authorized = (await internal?.(
      { headers: { "x-admin-token": "admin-token" } },
      { code: (statusCode) => statusCodes.push(statusCode), header: () => undefined },
    )) as { data: Array<Record<string, unknown>> };

    const rawMetrics = (await prometheus?.(
      { headers: { "x-admin-token": "admin-token" } },
      {
        code: (statusCode) => statusCodes.push(statusCode),
        header: (key, value) => {
          headers[key] = value;
        },
      },
    )) as string;

    expect(statusCodes).toEqual([401, 401]);
    expect(unauthorized.error).toBe("unauthorized");
    expect(unauthorizedPrometheus.error).toBe("unauthorized");
    expect(authorized.data[0].detail).toBe("timeout");
    expect(authorized.data[0].circuit).toEqual({ state: "OPEN", failures: 5, lastError: "timeout" });
    expect(headers["content-type"]).toContain("text/plain");
    expect(rawMetrics).toContain("hive_provider_requests_total");
  });
});
