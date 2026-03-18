import { describe, expect, it, beforeAll, afterAll } from "vitest";
import Fastify, { type FastifyInstance } from "fastify";
import { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import { v1Plugin } from "../../src/routes/v1-plugin";

function createTestApp() {
  return Fastify({
    ajv: { customOptions: { removeAdditional: false } },
  }).withTypeProvider<TypeBoxTypeProvider>();
}

// Minimal mock services that prevent auth/service crashes.
// Routes will fail at auth (401) for valid payloads, but validation
// happens BEFORE auth, so 400 for invalid payloads proves schema works.
const mockServices = {
  rateLimiter: { allow: async () => true },
  ai: {
    chatCompletions: async () => ({ error: "mock" }),
    imageGeneration: async () => ({ error: "mock" }),
    responses: async () => ({ error: "mock" }),
  },
  models: { list: () => [] },
  apiKeys: { verifyKey: async () => null },
  users: {},
  settings: { get: async () => null },
} as any;

// ---------------------------------------------------------------------------
// Extra fields rejected with 400
// ---------------------------------------------------------------------------

describe("TypeBox validation -- extra fields rejected", () => {
  let app: FastifyInstance;

  beforeAll(async () => {
    app = createTestApp();
    await app.register(v1Plugin, { services: mockServices });
    await app.ready();
  });

  afterAll(() => app.close());

  it("POST /v1/chat/completions rejects unknown field with 400", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/chat/completions",
      payload: { model: "gpt-4o", unknown_field: true },
      headers: { "content-type": "application/json" },
    });
    expect(res.statusCode).toBe(400);
    const body = res.json();
    expect(body.error).toBeDefined();
    expect(body.error.type).toBe("invalid_request_error");
    expect(body.error).toHaveProperty("message");
    expect(body.error).toHaveProperty("param");
    expect(body.error).toHaveProperty("code");
  });

  it("POST /v1/images/generations rejects unknown field with 400", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/images/generations",
      payload: { prompt: "a cat", bogus: "value" },
      headers: { "content-type": "application/json" },
    });
    expect(res.statusCode).toBe(400);
    expect(res.json().error.type).toBe("invalid_request_error");
  });

  it("POST /v1/responses rejects unknown field with 400", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/responses",
      payload: { input: "hello", extra: 123 },
      headers: { "content-type": "application/json" },
    });
    expect(res.statusCode).toBe(400);
    expect(res.json().error.type).toBe("invalid_request_error");
  });
});

// ---------------------------------------------------------------------------
// Valid requests pass validation (do NOT return 400)
// ---------------------------------------------------------------------------

describe("TypeBox validation -- valid requests pass", () => {
  let app: FastifyInstance;

  beforeAll(async () => {
    app = createTestApp();
    await app.register(v1Plugin, { services: mockServices });
    await app.ready();
  });

  afterAll(() => app.close());

  it("POST /v1/chat/completions with valid body does NOT return 400", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/chat/completions",
      payload: { model: "gpt-4o", messages: [{ role: "user", content: "hi" }] },
      headers: { "content-type": "application/json" },
    });
    // Should NOT be 400 (will be 401 from auth mock, which proves validation passed)
    expect(res.statusCode).not.toBe(400);
  });

  it("POST /v1/images/generations with valid body does NOT return 400", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/images/generations",
      payload: { prompt: "a sunset", model: "dall-e-3" },
      headers: { "content-type": "application/json" },
    });
    expect(res.statusCode).not.toBe(400);
  });

  it("POST /v1/responses with valid body does NOT return 400", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/responses",
      payload: { input: "hello world" },
      headers: { "content-type": "application/json" },
    });
    expect(res.statusCode).not.toBe(400);
  });

  it("GET /v1/models with no body succeeds", async () => {
    const res = await app.inject({
      method: "GET",
      url: "/v1/models",
    });
    expect(res.statusCode).toBe(200);
    expect(res.json()).toHaveProperty("object", "list");
  });
});

// ---------------------------------------------------------------------------
// Error format matches Phase 1 OpenAI format (all 4 fields)
// ---------------------------------------------------------------------------

describe("TypeBox validation -- error format matches Phase 1", () => {
  let app: FastifyInstance;

  beforeAll(async () => {
    app = createTestApp();
    await app.register(v1Plugin, { services: mockServices });
    await app.ready();
  });

  afterAll(() => app.close());

  it("validation error has all 4 OpenAI error fields", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/chat/completions",
      payload: { totally_invalid: true },
      headers: { "content-type": "application/json" },
    });
    expect(res.statusCode).toBe(400);
    const body = res.json();
    expect(Object.keys(body.error).sort()).toEqual(["code", "message", "param", "type"]);
    expect(body.error.type).toBe("invalid_request_error");
    expect(typeof body.error.message).toBe("string");
  });

  it("nested unknown field in messages is rejected", async () => {
    const res = await app.inject({
      method: "POST",
      url: "/v1/chat/completions",
      payload: {
        model: "gpt-4o",
        messages: [{ role: "user", content: "hi", mood: "happy" }],
      },
      headers: { "content-type": "application/json" },
    });
    expect(res.statusCode).toBe(400);
    expect(res.json().error.type).toBe("invalid_request_error");
  });
});
