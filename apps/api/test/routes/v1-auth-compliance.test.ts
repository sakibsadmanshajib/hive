import { describe, it, expect, beforeAll, afterAll } from "vitest";
import OpenAI from "openai";
import { createTestApp, createMockServices } from "../helpers/test-app";
import type { FastifyInstance } from "fastify";

const VALID_KEY = "sk-test-valid";
const USER_ID = "user_test";

describe("v1 auth compliance (FOUND-02) and Content-Type (FOUND-05)", () => {
  let app: FastifyInstance;
  let address: string;

  beforeAll(async () => {
    const result = await createTestApp(createMockServices(VALID_KEY, USER_ID));
    app = result.app;
    address = result.address;
  });

  afterAll(async () => {
    await app.close();
  });

  // --- FOUND-02: Auth compliance ---

  it("valid bearer token authenticates successfully", async () => {
    const client = new OpenAI({ apiKey: VALID_KEY, baseURL: `${address}/v1` });
    const response = await client.models.list();
    expect(response.data).toBeDefined();
    expect(Array.isArray(response.data)).toBe(true);
  });

  it("GET /v1/models without Bearer auth returns 401 authentication_error", async () => {
    const response = await fetch(`${address}/v1/models`);

    expect(response.status).toBe(401);

    const body = await response.json();
    expect(body).toEqual({
      error: {
        message: "No API key provided",
        type: "authentication_error",
        param: null,
        code: "invalid_api_key",
      },
    });
  });

  it("GET /v1/models with an invalid Bearer auth returns 401 authentication_error", async () => {
    const response = await fetch(`${address}/v1/models`, {
      headers: { Authorization: "Bearer sk-wrong-key" },
    });

    expect(response.status).toBe(401);

    const body = await response.json();
    expect(body).toEqual({
      error: {
        message: "Incorrect API key provided",
        type: "authentication_error",
        param: null,
        code: "invalid_api_key",
      },
    });
  });

  it("GET /v1/models/:model without Bearer auth returns 401 authentication_error", async () => {
    const response = await fetch(`${address}/v1/models/mock-chat`);

    expect(response.status).toBe(401);

    const body = await response.json();
    expect(body).toEqual({
      error: {
        message: "No API key provided",
        type: "authentication_error",
        param: null,
        code: "invalid_api_key",
      },
    });
  });

  it("GET /v1/models/:model with an invalid Bearer auth returns 401 authentication_error", async () => {
    const response = await fetch(`${address}/v1/models/mock-chat`, {
      headers: { Authorization: "Bearer sk-wrong-key" },
    });

    expect(response.status).toBe(401);

    const body = await response.json();
    expect(body).toEqual({
      error: {
        message: "Incorrect API key provided",
        type: "authentication_error",
        param: null,
        code: "invalid_api_key",
      },
    });
  });

  it("missing Authorization header returns 401 with correct error body", async () => {
    const response = await fetch(`${address}/v1/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model: "test", messages: [] }),
    });

    expect(response.status).toBe(401);

    const body = await response.json();
    expect(body).toEqual({
      error: {
        message: "No API key provided",
        type: "authentication_error",
        param: null,
        code: "invalid_api_key",
      },
    });
  });

  it("invalid bearer token returns 401 with correct error body", async () => {
    const client = new OpenAI({ apiKey: "sk-wrong-key", baseURL: `${address}/v1` });

    try {
      await client.chat.completions.create({ model: "test", messages: [] });
      expect.unreachable("should have thrown");
    } catch (error) {
      expect(error).toBeInstanceOf(OpenAI.AuthenticationError);
      const authError = error as OpenAI.AuthenticationError;
      expect(authError.status).toBe(401);
      expect((authError as any).error).toMatchObject({
        message: "Incorrect API key provided",
        type: "authentication_error",
        code: "invalid_api_key",
      });
    }
  });

  it("x-api-key header is ignored when Bearer is present on /v1/* routes", async () => {
    const response = await fetch(`${address}/v1/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${VALID_KEY}`,
        "x-api-key": "sk-wrong-key",
      },
      body: JSON.stringify({ model: "test", messages: [] }),
    });

    // Auth passed (Bearer is valid) — response should NOT be 401
    expect(response.status).not.toBe(401);
  });

  it("SDK throws AuthenticationError not generic APIError for 401", async () => {
    const client = new OpenAI({ apiKey: "sk-bad", baseURL: `${address}/v1` });

    try {
      await client.chat.completions.create({ model: "test", messages: [] });
      expect.unreachable("should have thrown");
    } catch (error) {
      expect(error).toBeInstanceOf(OpenAI.AuthenticationError);
      // AuthenticationError extends APIError
      expect(error).toBeInstanceOf(OpenAI.APIError);
    }
  });

  // --- FOUND-05: Content-Type ---

  it("non-streaming response has Content-Type: application/json", async () => {
    const response = await fetch(`${address}/v1/models`, {
      headers: { Authorization: `Bearer ${VALID_KEY}` },
    });

    expect(response.status).toBe(200);
    const contentType = response.headers.get("content-type");
    expect(contentType).toContain("application/json");
  });
});
