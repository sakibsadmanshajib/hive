import { describe, expect, it } from "vitest";
import { registerRoutes } from "../../src/routes";

class FakeApp {
  readonly handlers = new Map<string, (request?: any, reply?: any) => Promise<unknown>>();

  get(path: string, handler: (request?: any, reply?: any) => Promise<unknown>) {
    this.handlers.set(`GET ${path}`, handler);
  }

  post(path: string, handler: (request?: any, reply?: any) => Promise<unknown>) {
    this.handlers.set(`POST ${path}`, handler);
  }

  register(plugin: (app: any, opts: any) => Promise<void>, opts: any) {
    return plugin(this, opts);
  }

  addHook() {}
  setErrorHandler() {}
  setNotFoundHandler() {}
}

function createReply() {
  let statusCode = 200;
  let sentPayload: unknown;

  return {
    get statusCode() {
      return statusCode;
    },
    get sentPayload() {
      return sentPayload;
    },
    code(status: number) {
      statusCode = status;
      return this;
    },
    send(payload: unknown) {
      sentPayload = payload;
      return payload;
    },
    header() {
      return this;
    },
  };
}

describe("analytics internal route", () => {
  it("registers an admin-only internal analytics route", () => {
    const app = new FakeApp();

    registerRoutes(app as never, {} as never);

    expect(app.handlers.has("GET /v1/analytics/internal")).toBe(true);
  });

  it("returns admin-only analytics split by api and web channels", async () => {
    const app = new FakeApp();
    registerRoutes(app as never, {
      env: { adminStatusToken: "admin-token" },
      usage: {
        trafficAnalytics: async () => ({
          windowDays: 7,
          channels: {
            api: { requests: 12, credits: 120 },
            web: { requests: 5, credits: 0 },
          },
          byApiKey: [{ key: "key-123", requests: 10, credits: 100 }],
          webBreakdown: {
            guestRequests: 3,
            authenticatedRequests: 2,
            guestSessions: 4,
            linkedGuests: 1,
            conversionRate: 0.25,
          },
        }),
      },
    } as never);

    const handler = app.handlers.get("GET /v1/analytics/internal");
    const reply = createReply();

    const payload = await handler?.(
      { headers: { "x-admin-token": "admin-token" } },
      reply,
    ) as {
      object: string;
      data: {
        channels: { api: { requests: number }; web: { requests: number } };
        webBreakdown: { linkedGuests: number };
      };
    };

    expect(reply.statusCode).toBe(200);
    expect(payload.object).toBe("analytics.traffic");
    expect(payload.data.channels.api.requests).toBe(12);
    expect(payload.data.channels.web.requests).toBe(5);
    expect(payload.data.webBreakdown.linkedGuests).toBe(1);
  });

  it("rejects analytics requests without a valid admin token", async () => {
    const app = new FakeApp();
    registerRoutes(app as never, {
      env: { adminStatusToken: "admin-token" },
      usage: {
        trafficAnalytics: async () => ({
          windowDays: 7,
          channels: {
            api: { requests: 0, credits: 0 },
            web: { requests: 0, credits: 0 },
          },
          byApiKey: [],
          webBreakdown: {
            guestRequests: 0,
            authenticatedRequests: 0,
            guestSessions: 0,
            linkedGuests: 0,
            conversionRate: 0,
          },
        }),
      },
    } as never);

    const handler = app.handlers.get("GET /v1/analytics/internal");
    const reply = createReply();

    const payload = await handler?.({ headers: {} }, reply);

    expect(reply.statusCode).toBe(401);
    expect(payload).toEqual({ error: "unauthorized" });
  });
});
