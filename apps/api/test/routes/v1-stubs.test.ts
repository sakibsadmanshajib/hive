import { describe, it, expect, beforeAll, afterAll } from "vitest";
import type { FastifyInstance } from "fastify";
import type { HTTPMethods } from "fastify";
import { createTestApp, createMockServices } from "../helpers/test-app";

let app: FastifyInstance;

beforeAll(async () => {
  const result = await createTestApp(createMockServices("valid-api-key", "user-1"));
  app = result.app;
});

afterAll(async () => {
  await app.close();
});

describe("OPS-01: Stub endpoint error format", () => {
  const stubEndpoints: [HTTPMethods, string][] = [
    ["POST", "/v1/audio/speech"],
    ["GET", "/v1/files"],
    ["POST", "/v1/uploads"],
    ["GET", "/v1/batches"],
    ["POST", "/v1/completions"],
    ["POST", "/v1/fine_tuning/jobs"],
    ["POST", "/v1/moderations"],
  ];

  it.each(stubEndpoints)(
    "%s %s returns 404 with OpenAI stub error format",
    async (method, url) => {
      const response = await app.inject({ method, url });
      expect(response.statusCode).toBe(404);

      const body: { error: { type: string; code: string; param: null; message: string } } =
        response.json();
      expect(body.error).toBeDefined();
      expect(body.error.type).toBe("not_found_error");
      expect(body.error.code).toBe("unsupported_endpoint");
      expect(body.error.param).toBeNull();
      expect(body.error.message).toContain("not yet supported");
    },
  );

  it("DELETE /v1/files/:file_id returns stub error for parameterized routes", async () => {
    const response = await app.inject({
      method: "DELETE",
      url: "/v1/files/test-file-id",
    });
    expect(response.statusCode).toBe(404);

    const body: { error: { type: string; code: string; param: null; message: string } } =
      response.json();
    expect(body.error.type).toBe("not_found_error");
    expect(body.error.code).toBe("unsupported_endpoint");
    expect(body.error.param).toBeNull();
    expect(body.error.message).toContain("not yet supported");
  });

  it("error message includes the specific endpoint path", async () => {
    const response = await app.inject({
      method: "POST",
      url: "/v1/audio/speech",
    });
    const body: { error: { message: string } } = response.json();
    expect(body.error.message).toContain("/v1/audio/speech");
  });

  it("POST /v1/chat/completions does NOT return stub error (regression guard)", async () => {
    const response = await app.inject({
      method: "POST",
      url: "/v1/chat/completions",
      headers: { "content-type": "application/json" },
      payload: JSON.stringify({ model: "gpt-4o-mini", messages: [] }),
    });

    // It may return 400/401/other errors due to missing auth/body,
    // but it must NOT return the stub 404 with "unsupported_endpoint"
    if (response.statusCode === 404) {
      const body: { error: { code: string } } = response.json();
      expect(body.error.code).not.toBe("unsupported_endpoint");
    }
  });
});
