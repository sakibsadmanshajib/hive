import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { OpenRouterProviderClient } from "../../src/providers/openrouter-client";
import type { ProviderChatRequest } from "../../src/providers/types";

// Mock the http-client module
vi.mock("../../src/providers/http-client", () => ({
  fetchWithRetry: vi.fn(async ({ url, init }) => {
    if (url.includes("/models")) {
      return {
        ok: true,
        status: 200,
        statusText: "OK",
      };
    }
    
    if (url.includes("/chat/completions")) {
      return {
        ok: true,
        status: 200,
        statusText: "OK",
        json: async () => ({
          choices: [
            {
              message: {
                content: "Hello from OpenRouter!",
              },
            },
          ],
          model: "openai/gpt-3.5-turbo",
          usage: {
            prompt_tokens: 10,
            completion_tokens: 5,
            total_tokens: 15,
          },
        }),
      };
    }
    
    return {
      ok: false,
      status: 500,
      statusText: "Internal Server Error",
    };
  }),
}));

describe("OpenRouterProviderClient", () => {
  let originalEnv: NodeJS.ProcessEnv;

  beforeEach(() => {
    originalEnv = { ...process.env };
    process.env.OPENROUTER_API_KEY = "test-api-key";
    process.env.OPENROUTER_BASE_URL = "https://openrouter.ai/api/v1";
  });

  afterEach(() => {
    process.env = originalEnv;
    vi.clearAllMocks();
  });

  it("should be enabled when API key and base URL are configured", () => {
    const client = new OpenRouterProviderClient({
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "test-api-key",
      timeoutMs: 5000,
      maxRetries: 2,
    });
    expect(client.isEnabled()).toBe(true);
  });

  it("should be disabled when API key is missing", () => {
    const client = new OpenRouterProviderClient({
      baseUrl: "https://openrouter.ai/api/v1",
      timeoutMs: 5000,
      maxRetries: 2,
    });
    expect(client.isEnabled()).toBe(false);
  });

  it("should return healthy status when API is accessible", async () => {
    const client = new OpenRouterProviderClient({
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "test-api-key",
      timeoutMs: 5000,
      maxRetries: 2,
    });

    const status = await client.status();
    expect(status.enabled).toBe(true);
    expect(status.healthy).toBe(true);
    expect(status.detail).toBe("reachable");
  });

  it("should handle chat requests correctly", async () => {
    const client = new OpenRouterProviderClient({
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "test-api-key",
      timeoutMs: 5000,
      maxRetries: 2,
    });

    const request: ProviderChatRequest = {
      model: "openai/gpt-3.5-turbo",
      messages: [
        { role: "user", content: "Hello" },
      ],
    };

    const response = await client.chat(request);
    expect(response.content).toBe("Hello from OpenRouter!");
    expect(response.providerModel).toBe("openai/gpt-3.5-turbo");
    expect(response.usage).toBeDefined();
    expect(response.usage?.promptTokens).toBe(10);
    expect(response.usage?.completionTokens).toBe(5);
    expect(response.usage?.totalTokens).toBe(15);
  });

  it("should throw error when API key is missing for chat", async () => {
    const client = new OpenRouterProviderClient({
      baseUrl: "https://openrouter.ai/api/v1",
      timeoutMs: 5000,
      maxRetries: 2,
    });

    const request: ProviderChatRequest = {
      model: "openai/gpt-3.5-turbo",
      messages: [
        { role: "user", content: "Hello" },
      ],
    };

    await expect(client.chat(request)).rejects.toThrow("openrouter api key missing");
  });

  it("should include required OpenRouter headers", async () => {
    const client = new OpenRouterProviderClient({
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "test-api-key",
      timeoutMs: 5000,
      maxRetries: 2,
    });

    const request: ProviderChatRequest = {
      model: "openai/gpt-3.5-turbo",
      messages: [
        { role: "user", content: "Hello" },
      ],
    };

    await client.chat(request);

    const { fetchWithRetry } = await import("../../src/providers/http-client");
    expect(fetchWithRetry).toHaveBeenCalledWith(
      expect.objectContaining({
        init: expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: "Bearer test-api-key",
            "Content-Type": "application/json",
            "HTTP-Referer": "https://hive-ai.com",
            "X-Title": "Hive AI Gateway",
          }),
        }),
      })
    );
  });

  it("should have correct provider name", () => {
    const client = new OpenRouterProviderClient({
      baseUrl: "https://openrouter.ai/api/v1",
      apiKey: "test-api-key",
      timeoutMs: 5000,
      maxRetries: 2,
    });
    expect(client.name).toBe("openrouter");
  });
});