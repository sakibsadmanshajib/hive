import { describe, expect, it, vi, beforeAll, afterAll } from "vitest";
import OpenAI from "openai";
import type { FastifyInstance } from "fastify";
import { registerModelsRoute } from "../../src/routes/models";
import { createTestApp, createMockServices } from "../helpers/test-app";

/* ------------------------------------------------------------------ */
/*  Part A: Unit tests using FakeApp pattern                          */
/* ------------------------------------------------------------------ */

class FakeApp {
  readonly handlers = new Map<string, { handler: Function; opts?: any }>();

  get(path: string, ...args: any[]) {
    if (args.length === 2) {
      this.handlers.set(`GET ${path}`, { handler: args[1], opts: args[0] });
    } else {
      this.handlers.set(`GET ${path}`, { handler: args[0] });
    }
  }
}

const mockModels = [
  {
    id: "test-model",
    object: "model" as const,
    created: 1700000000,
    capability: "chat" as const,
    costType: "variable" as const,
    pricing: { creditsPerRequest: 5 },
  },
];

const mockServices = {
  models: {
    list: () => mockModels,
    findById: (id: string) => mockModels.find((m) => m.id === id),
  },
} as never;

describe("models route", () => {
  const app = new FakeApp();
  registerModelsRoute(app as never, mockServices);

  it("list returns object: list with data array", async () => {
    const entry = app.handlers.get("GET /v1/models");
    const result = await entry!.handler();
    expect(result).toEqual({
      object: "list",
      data: expect.any(Array),
    });
    expect(result.data.length).toBe(1);
  });

  it("each model in list has exactly id, object, created, owned_by fields", async () => {
    const entry = app.handlers.get("GET /v1/models");
    const result = await entry!.handler();
    for (const item of result.data) {
      expect(Object.keys(item).sort()).toEqual(["created", "id", "object", "owned_by"]);
    }
  });

  it("list does not leak internal fields", async () => {
    const entry = app.handlers.get("GET /v1/models");
    const result = await entry!.handler();
    for (const item of result.data) {
      expect(item).not.toHaveProperty("capability");
      expect(item).not.toHaveProperty("costType");
      expect(item).not.toHaveProperty("pricing");
    }
  });

  it("retrieve returns single model object", async () => {
    const entry = app.handlers.get("GET /v1/models/:model");
    const result = await entry!.handler(
      { params: { model: "test-model" } },
      { code: vi.fn().mockReturnThis(), send: vi.fn() },
    );
    expect(result).toEqual({
      id: "test-model",
      object: "model",
      created: 1700000000,
      owned_by: "hive",
    });
  });

  it("retrieve unknown model returns 404 error", async () => {
    const entry = app.handlers.get("GET /v1/models/:model");
    const reply = { code: vi.fn().mockReturnThis(), send: vi.fn() };
    await entry!.handler({ params: { model: "nonexistent" } }, reply);
    expect(reply.code).toHaveBeenCalledWith(404);
    expect(reply.send).toHaveBeenCalledWith({
      error: {
        message: expect.stringContaining("does not exist"),
        type: "invalid_request_error",
        param: null,
        code: "model_not_found",
      },
    });
  });
});

/* ------------------------------------------------------------------ */
/*  Part B: SDK integration tests                                     */
/* ------------------------------------------------------------------ */

describe("models SDK integration (FOUND-03, FOUND-04)", () => {
  let app: FastifyInstance;
  let address: string;

  beforeAll(async () => {
    const result = await createTestApp(createMockServices("sk-test", "user_test"));
    app = result.app;
    address = result.address;
  });

  afterAll(async () => {
    await app.close();
  });

  it("client.models.list() returns model objects", async () => {
    const client = new OpenAI({ apiKey: "sk-test", baseURL: address + "/v1" });
    const response = await client.models.list();
    const models = [];
    for await (const model of response) {
      models.push(model);
    }
    expect(models.length).toBeGreaterThan(0);
    for (const model of models) {
      expect(model.id).toEqual(expect.any(String));
      expect(model.object).toBe("model");
      expect(model.created).toEqual(expect.any(Number));
      expect(model.owned_by).toEqual(expect.any(String));
    }
  });

  it("client.models.retrieve() returns single model", async () => {
    const client = new OpenAI({ apiKey: "sk-test", baseURL: address + "/v1" });
    const response = await client.models.retrieve("mock-chat");
    expect(response.id).toBe("mock-chat");
    expect(response.object).toBe("model");
    expect(response.created).toEqual(expect.any(Number));
    expect(response.owned_by).toEqual(expect.any(String));
  });

  it("client.models.retrieve() throws NotFoundError for unknown model", async () => {
    const client = new OpenAI({ apiKey: "sk-test", baseURL: address + "/v1" });
    try {
      await client.models.retrieve("nonexistent");
      expect.fail("Expected NotFoundError");
    } catch (err) {
      expect(err).toBeInstanceOf(OpenAI.NotFoundError);
      expect((err as OpenAI.NotFoundError).status).toBe(404);
    }
  });
});
