import { describe, expect, it } from "vitest";
import Fastify from "fastify";
import { sendApiError, STATUS_TO_TYPE, type OpenAIErrorType } from "../../src/routes/api-error";
import { v1Plugin } from "../../src/routes/v1-plugin";

// ---------------------------------------------------------------------------
// sendApiError unit tests
// ---------------------------------------------------------------------------

describe("sendApiError", () => {
  it("produces OpenAI error shape for 400", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 400, "bad request");
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    expect(res.statusCode).toBe(400);
    const body = res.json();
    expect(body).toEqual({
      error: {
        message: "bad request",
        type: "invalid_request_error",
        param: null,
        code: null,
      },
    });
  });

  it("produces authentication_error for 401 with custom code", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 401, "invalid", { code: "invalid_api_key" });
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    expect(res.statusCode).toBe(401);
    const body = res.json();
    expect(body.error.type).toBe("authentication_error");
    expect(body.error.code).toBe("invalid_api_key");
  });

  it("produces insufficient_quota for 402", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 402, "no credits");
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    expect(res.statusCode).toBe(402);
    expect(res.json().error.type).toBe("insufficient_quota");
  });

  it("produces permission_error for 403", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 403, "forbidden");
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    expect(res.statusCode).toBe(403);
    expect(res.json().error.type).toBe("permission_error");
  });

  it("produces not_found_error for 404", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 404, "not found");
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    expect(res.statusCode).toBe(404);
    expect(res.json().error.type).toBe("not_found_error");
  });

  it("produces rate_limit_error for 429", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 429, "rate limited");
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    expect(res.statusCode).toBe(429);
    expect(res.json().error.type).toBe("rate_limit_error");
  });

  it("produces server_error for 500", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 500, "internal");
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    expect(res.statusCode).toBe(500);
    expect(res.json().error.type).toBe("server_error");
  });

  it("always includes all four fields even when null", async () => {
    const app = Fastify();
    app.get("/test", (_req, reply) => {
      sendApiError(reply, 400, "test");
    });
    const res = await app.inject({ method: "GET", url: "/test" });
    const body = res.json();
    expect(Object.keys(body.error).sort()).toEqual(["code", "message", "param", "type"]);
    expect(body.error.param).toBeNull();
    expect(body.error.code).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// v1Plugin scoped error handler tests
// ---------------------------------------------------------------------------

describe("v1Plugin", () => {
  it("returns 404 with OpenAI format for unknown /v1/* routes", async () => {
    const app = Fastify();
    await app.register(v1Plugin, { services: {} as any });
    await app.ready();

    const res = await app.inject({ method: "POST", url: "/v1/nonexistent-route" });
    expect(res.statusCode).toBe(404);
    const body = res.json();
    expect(body.error.message).toContain("Unknown API route");
    expect(body.error.type).toBe("not_found_error");
    expect(body.error.param).toBeNull();
    expect(body.error.code).toBeNull();
  });

  it("catches thrown errors and returns 500 with OpenAI format", async () => {
    const app = Fastify();
    await app.register(v1Plugin, { services: {} as any });
    app.post("/v1/test-route", async () => {
      throw new Error("boom");
    });
    await app.ready();

    const res = await app.inject({ method: "POST", url: "/v1/test-route" });
    expect(res.statusCode).toBe(500);
    const body = res.json();
    expect(body.error.message).toBe("boom");
    expect(body.error.type).toBe("server_error");
    expect(body.error.param).toBeNull();
    expect(body.error.code).toBeNull();
  });

  it("returns 400 with invalid_request_error for malformed JSON body", async () => {
    const app = Fastify();
    await app.register(v1Plugin, { services: {} as any });
    app.post("/v1/test-route", async () => {
      return { ok: true };
    });
    await app.ready();

    const res = await app.inject({
      method: "POST",
      url: "/v1/test-route",
      headers: { "content-type": "application/json" },
      payload: "not valid json{{{",
    });
    expect(res.statusCode).toBe(400);
    const body = res.json();
    expect(body.error.type).toBe("invalid_request_error");
    expect(body.error.param).toBeNull();
    expect(body.error.code).toBeNull();
  });

  it("does not affect non-v1 routes outside the plugin", async () => {
    const app = Fastify();

    // Register plugin in a scoped context
    await app.register(v1Plugin, { services: {} as any });

    // Register a non-v1 route OUTSIDE the plugin scope
    app.get("/health", async () => {
      throw new Error("health error");
    });
    await app.ready();

    const res = await app.inject({ method: "GET", url: "/health" });
    // Fastify default error handler returns { statusCode, error, message } - NOT OpenAI format
    const body = res.json();
    expect(body.error).not.toHaveProperty("type");
    // Default Fastify shape has `statusCode` at top-level
    expect(body).toHaveProperty("statusCode");
  });
});
