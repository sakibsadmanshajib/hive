import { describe, expect, it } from "vitest";
import type { FastifyInstance, LightMyRequestResponse } from "fastify";
import type { MockAiService } from "../helpers/test-app";
import { createMockServices, createTestApp } from "../helpers/test-app";

const VALID_API_KEY = "valid-api-key";
const INVALID_API_KEY = "invalid-api-key";
const USER_ID = "user-1";

const noDispatchHeaders = {
  "x-model-routed": "",
  "x-provider-used": "",
  "x-provider-model": "",
  "x-actual-credits": "0",
} as const;

const authCases = [
  {
    route: "/v1/chat/completions",
    body: { model: "mock-chat", messages: [{ role: "user", content: "hi" }] },
  },
  {
    route: "/v1/embeddings",
    body: { model: "text-embedding-3-small", input: "hello" },
  },
  {
    route: "/v1/images/generations",
    body: { model: "dall-e-3", prompt: "a lighthouse in fog" },
  },
  {
    route: "/v1/responses",
    body: { model: "mock-chat", input: "hello" },
  },
] as const;

const serviceErrorCases: Array<{
  route: string;
  body: Record<string, unknown>;
  createOverrides: () => Partial<MockAiService>;
}> = [
  {
    route: "/v1/chat/completions",
    body: { model: "mock-chat", messages: [{ role: "user", content: "hi" }] },
    createOverrides: () => ({
      chatCompletions: async () => ({ error: "provider unavailable", statusCode: 502 as const }),
    }),
  },
  {
    route: "/v1/embeddings",
    body: { model: "text-embedding-3-small", input: "hello" },
    createOverrides: () => ({
      embeddings: async () => ({ error: "provider unavailable", statusCode: 502 as const }),
    }),
  },
  {
    route: "/v1/images/generations",
    body: { model: "dall-e-3", prompt: "a lighthouse in fog" },
    createOverrides: () => ({
      imageGeneration: async () => ({ error: "provider unavailable", statusCode: 502 as const }),
    }),
  },
  {
    route: "/v1/responses",
    body: { model: "mock-chat", input: "hello" },
    createOverrides: () => ({
      responses: async () => ({ error: "provider unavailable", statusCode: 502 as const }),
    }),
  },
];

function expectNoDispatchHeaders(response: LightMyRequestResponse): void {
  expect(response.headers["x-model-routed"]).toBe(noDispatchHeaders["x-model-routed"]);
  expect(response.headers["x-provider-used"]).toBe(noDispatchHeaders["x-provider-used"]);
  expect(response.headers["x-provider-model"]).toBe(noDispatchHeaders["x-provider-model"]);
  expect(response.headers["x-actual-credits"]).toBe(noDispatchHeaders["x-actual-credits"]);
}

async function withTestApp(
  aiOverrides: Partial<MockAiService>,
  run: (app: FastifyInstance) => Promise<void>,
): Promise<void> {
  const { app } = await createTestApp(createMockServices(VALID_API_KEY, USER_ID, aiOverrides));
  try {
    await run(app);
  } finally {
    await app.close();
  }
}

describe("DIFF-01: v1 route error headers", () => {
  it.each(authCases)(
    "returns static no-dispatch DIFF headers on invalid auth for %s",
    async ({ route, body }) => {
      await withTestApp({}, async (app) => {
        const response = await app.inject({
          method: "POST",
          url: route,
          headers: {
            authorization: `Bearer ${INVALID_API_KEY}`,
            "content-type": "application/json",
          },
          payload: body,
        });

        expect(response.statusCode).toBe(401);
        expectNoDispatchHeaders(response);
      });
    },
  );

  it.each(serviceErrorCases)(
    "returns static no-dispatch DIFF headers on service errors for %s",
    async ({ route, body, createOverrides }) => {
      await withTestApp(createOverrides(), async (app) => {
        const response = await app.inject({
          method: "POST",
          url: route,
          headers: {
            authorization: `Bearer ${VALID_API_KEY}`,
            "content-type": "application/json",
          },
          payload: body,
        });

        expect(response.statusCode).toBe(502);
        expectNoDispatchHeaders(response);
      });
    },
  );
});
