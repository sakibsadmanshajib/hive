import { describe, expect, it } from "vitest";
import { registerProvidersStatusRoute } from "../../src/routes/providers-status";

type RegisteredHandler = (
  request?: { headers?: Record<string, string> },
  reply?: { code: (statusCode: number) => unknown },
) => Promise<unknown>;

class FakeApp {
  handlers = new Map<string, RegisteredHandler>();

  get(path: string, handler: RegisteredHandler) {
    this.handlers.set(path, handler);
  }
}

describe("providers status routes", () => {
  it("returns sanitized public status without details", async () => {
    const app = new FakeApp();
    registerProvidersStatusRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      ai: {
        providersStatus: async () => ({
          providers: [
            {
              name: "ollama",
              enabled: true,
              healthy: true,
              detail: "reachable",
              circuit: { state: "CLOSED", failures: 0 },
            },
            {
              name: "groq",
              enabled: false,
              healthy: false,
              detail: "missing key",
              circuit: { state: "CLOSED", failures: 0 },
            },
            {
              name: "mock",
              enabled: true,
              healthy: false,
              detail: "timeout",
              circuit: { state: "OPEN", failures: 5 },
            },
          ],
        }),
      },
    } as never);

    const handler = app.handlers.get("/v1/providers/status");
    const payload = (await handler?.()) as { data: Array<Record<string, string | boolean>> };

    expect(payload.data[0]).toEqual({ name: "ollama", enabled: true, healthy: true, state: "ready" });
    expect(payload.data[1]).toEqual({ name: "groq", enabled: false, healthy: false, state: "disabled" });
    expect(payload.data[2]).toEqual({ name: "mock", enabled: true, healthy: false, state: "circuit-open" });
    expect("detail" in payload.data[0]).toBe(false);
  });

  it("requires admin token for internal provider status endpoint", async () => {
    const app = new FakeApp();
    registerProvidersStatusRoute(app as never, {
      env: { adminStatusToken: "admin-token" },
      ai: {
        providersStatus: async () => ({
          providers: [
            {
              name: "mock",
              enabled: true,
              healthy: true,
              detail: "always available fallback",
              circuit: { state: "CLOSED", failures: 0 },
            },
          ],
        }),
      },
    } as never);

    const internal = app.handlers.get("/v1/providers/status/internal");
    const statusCodes: number[] = [];
    const unauthorized = (await internal?.(
      { headers: {} },
      { code: (statusCode) => statusCodes.push(statusCode) },
    )) as { error: string };
    const authorized = (await internal?.(
      { headers: { "x-admin-token": "admin-token" } },
      { code: (statusCode) => statusCodes.push(statusCode) },
    )) as {
      data: Array<Record<string, string | boolean>>;
    };

    expect(statusCodes).toEqual([401]);
    expect(unauthorized.error).toBe("unauthorized");
    expect(authorized.data[0].detail).toBe("always available fallback");
  });
});
