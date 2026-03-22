import { randomUUID } from "node:crypto";
import Fastify from "fastify";
import { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import { v1Plugin } from "../../src/routes/v1-plugin";
import type { ChatCompletionsBody } from "../../src/schemas/chat-completions";
import type { RuntimeServices } from "../../src/runtime/services";

// Discriminated union return types matching RuntimeAiService
type AiSuccess<T> = {
  statusCode: 200;
  body: T;
  headers: Record<string, string>;
};

type AiError = {
  error: string;
  statusCode: 400 | 402 | 429 | 422 | 502;
};

type AiStreamSuccess = {
  statusCode: 200;
  response: Response;
  headers: Record<string, string>;
};

type ChatCompletionBody = {
  id: string;
  object: "chat.completion";
  created: number;
  model: string;
  choices: Array<{
    index: number;
    finish_reason: "stop";
    message: { role: "assistant"; content: string; refusal: null };
    logprobs: null;
  }>;
  usage: { prompt_tokens: number; completion_tokens: number; total_tokens: number };
};

type EmbeddingBody = {
  object: "list";
  data: Array<{ object: "embedding"; index: number; embedding: number[] }>;
  model: string;
  usage: { prompt_tokens: number; total_tokens: number };
};

type ImagesBody = {
  created: number;
  data: Array<{ url?: string; b64_json?: string }>;
};

type ResponseBody = {
  id: string;
  object: "response";
  created_at: number;
  status: "completed";
  model: string;
  output: Array<{
    type: "message";
    id: string;
    role: "assistant";
    status: "completed";
    content: Array<{ type: "output_text"; text: string }>;
  }>;
  usage: { input_tokens: number; output_tokens: number; total_tokens: number };
};

type UsageContext = { channel: string; apiKeyId?: string };
type ChatCompletionsResult = Awaited<ReturnType<RuntimeServices["ai"]["chatCompletions"]>>;
type ChatCompletionsStreamResult = Awaited<ReturnType<RuntimeServices["ai"]["chatCompletionsStream"]>>;
type ImageGenerationRequest = Parameters<RuntimeServices["ai"]["imageGeneration"]>[1];
type ImageGenerationResult = Awaited<ReturnType<RuntimeServices["ai"]["imageGeneration"]>>;
type EmbeddingsRequest = Parameters<RuntimeServices["ai"]["embeddings"]>[1];
type EmbeddingsResult = Awaited<ReturnType<RuntimeServices["ai"]["embeddings"]>>;
type ResponsesRequest = Parameters<RuntimeServices["ai"]["responses"]>[1];
type ResponsesResult = Awaited<ReturnType<RuntimeServices["ai"]["responses"]>>;
type V1MockServices = Pick<
  RuntimeServices,
  "users" | "env" | "supabaseAuth" | "authz" | "userSettings" | "models" | "rateLimiter"
>;

export type MockAiService = {
  chatCompletions: (
    userId: string,
    body: ChatCompletionsBody,
    usageContext: UsageContext,
  ) => Promise<ChatCompletionsResult>;

  chatCompletionsStream: (
    userId: string,
    body: ChatCompletionsBody,
    usageContext: UsageContext,
  ) => Promise<ChatCompletionsStreamResult>;

  imageGeneration: (
    userId: string,
    request: ImageGenerationRequest,
    usageContext: UsageContext,
  ) => Promise<ImageGenerationResult>;

  embeddings: (
    userId: string,
    body: EmbeddingsRequest,
    usageContext: UsageContext,
  ) => Promise<EmbeddingsResult>;

  responses: (
    userId: string,
    body: ResponsesRequest,
    usageContext: UsageContext,
  ) => Promise<ResponsesResult>;
};

function makeDefaultHeaders(): Record<string, string> {
  return {
    "x-model-routed": "mock-chat",
    "x-provider-used": "mock-provider",
    "x-provider-model": "mock-model",
    "x-actual-credits": "1",
  };
}

function createDefaultMockAiService(): MockAiService {
  return {
    chatCompletions: async (_userId, _body, _ctx) => ({
      statusCode: 200 as const,
      headers: makeDefaultHeaders(),
      body: {
        id: `chatcmpl-${randomUUID().slice(0, 12)}`,
        object: "chat.completion" as const,
        created: Math.floor(Date.now() / 1000),
        model: "mock-chat",
        choices: [{
          index: 0,
          finish_reason: "stop" as const,
          message: { role: "assistant" as const, content: "Hello from mock", refusal: null },
          logprobs: null,
        }],
        usage: { prompt_tokens: 5, completion_tokens: 5, total_tokens: 10 },
      },
    }),

    chatCompletionsStream: async (_userId, _body, _ctx) => {
      // Build two SSE chunks + [DONE] into a ReadableStream
      const chunks = [
        `data: ${JSON.stringify({
          id: `chatcmpl-${randomUUID().slice(0, 12)}`,
          object: "chat.completion.chunk",
          created: Math.floor(Date.now() / 1000),
          model: "mock-chat",
          choices: [{ index: 0, delta: { role: "assistant", content: "Hello" }, finish_reason: null, logprobs: null }],
        })}\n\n`,
        `data: ${JSON.stringify({
          id: `chatcmpl-${randomUUID().slice(0, 12)}`,
          object: "chat.completion.chunk",
          created: Math.floor(Date.now() / 1000),
          model: "mock-chat",
          choices: [{ index: 0, delta: { content: " world" }, finish_reason: "stop", logprobs: null }],
        })}\n\n`,
        "data: [DONE]\n\n",
      ];
      const encoder = new TextEncoder();
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          for (const chunk of chunks) {
            controller.enqueue(encoder.encode(chunk));
          }
          controller.close();
        },
      });
      const response = new Response(stream, {
        headers: { "content-type": "text/event-stream" },
      });
      return {
        statusCode: 200 as const,
        response,
        headers: makeDefaultHeaders(),
      };
    },

    imageGeneration: async (_userId, _request, _ctx) => ({
      statusCode: 200 as const,
      headers: {
        "x-model-routed": "mock-image",
        "x-provider-used": "mock-provider",
        "x-provider-model": "mock-image-model",
        "x-actual-credits": "5",
      },
      body: {
        created: Math.floor(Date.now() / 1000),
        data: [{ url: "https://mock.example.com/image.png" }],
      },
    }),

    embeddings: async (_userId, body, _ctx) => ({
      statusCode: 200 as const,
      headers: {
        "x-model-routed": body.model,
        "x-provider-used": "mock-provider",
        "x-provider-model": "mock-embedding-model",
        "x-actual-credits": "1",
      },
      body: {
        object: "list" as const,
        data: [{ object: "embedding" as const, index: 0, embedding: [0.1, 0.2, 0.3] }],
        model: body.model,
        usage: { prompt_tokens: 2, total_tokens: 2 },
      },
    }),

    responses: async (_userId, _body, _ctx) => ({
      statusCode: 200 as const,
      headers: makeDefaultHeaders(),
      body: {
        id: `resp_${randomUUID().replace(/-/g, "").slice(0, 24)}`,
        object: "response" as const,
        created_at: Math.floor(Date.now() / 1000),
        status: "completed" as const,
        model: "mock-chat",
        output: [{
          type: "message" as const,
          id: `msg_${randomUUID().replace(/-/g, "").slice(0, 24)}`,
          role: "assistant" as const,
          status: "completed" as const,
          content: [{ type: "output_text" as const, text: "Mock response output" }],
        }],
        usage: { input_tokens: 5, output_tokens: 10, total_tokens: 15 },
      },
    }),
  };
}

export type MockServices = V1MockServices & {
  ai: MockAiService;
};

export function createMockServices(
  validApiKey: string,
  userId: string,
  aiOverrides?: Partial<MockAiService>,
  rateLimiterOverride?: { allow: () => Promise<boolean> },
): MockServices {
  const defaultAi = createDefaultMockAiService();
  return {
    users: {
      resolveApiKey: async (key: string) => {
        if (key === validApiKey) {
          return { userId, scopes: ["chat", "image", "usage", "billing"], apiKeyId: "key_test" };
        }
        return null;
      },
    },
    env: {
      allowDevApiKeyPrefix: false,
      supabase: { flags: { authEnabled: false } },
    },
    supabaseAuth: {
      getSessionPrincipal: async () => null,
    },
    authz: {
      requirePermission: async () => true,
    },
    userSettings: {
      getForUser: async () => ({ apiEnabled: true }),
      canUse: () => true,
    },
    models: {
      list: () => [
        { id: "mock-chat", object: "model", created: 1700000000, capability: "chat", costType: "paid" },
        { id: "text-embedding-3-small", object: "model", created: 1700000000, capability: "embedding", costType: "paid" },
        { id: "dall-e-3", object: "model", created: 1700000000, capability: "image", costType: "paid" },
      ],
      findById: (modelId: string) => {
        const models = [
          { id: "mock-chat", object: "model", created: 1700000000, capability: "chat", costType: "paid" },
          { id: "text-embedding-3-small", object: "model", created: 1700000000, capability: "embedding", costType: "paid" },
          { id: "dall-e-3", object: "model", created: 1700000000, capability: "image", costType: "paid" },
        ];
        return models.find((m) => m.id === modelId);
      },
    },
    rateLimiter: rateLimiterOverride ?? {
      allow: async () => true,
    },
    ai: { ...defaultAi, ...aiOverrides },
  };
}

export async function createTestApp(mockServices: MockServices) {
  const app = Fastify({
    logger: false,
    ajv: {
      customOptions: {
        removeAdditional: false,
      },
    },
  }).withTypeProvider<TypeBoxTypeProvider>();

  await app.register(v1Plugin, { services: mockServices as RuntimeServices });

  const address = await app.listen({ port: 0 });
  return { app, address };
}
