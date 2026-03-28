import { afterEach, describe, expect, it, vi } from "vitest";
import { OpenAIProviderClient } from "../../src/providers/openai-client";

describe("openai provider client image support", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("translates a chat completion request to the upstream chat endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          model: "gpt-4o-mini-2026-03-01",
          choices: [{ message: { content: "Chat reply" } }],
          usage: {
            prompt_tokens: 11,
            completion_tokens: 7,
            total_tokens: 18,
          },
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new OpenAIProviderClient({
      baseUrl: "https://api.openai.com/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(
      client.chat({
        model: "gpt-4o-mini",
        messages: [{ role: "user", content: "hello" }],
      }),
    ).resolves.toEqual({
      content: "Chat reply",
      providerModel: "gpt-4o-mini-2026-03-01",
      usage: {
        promptTokens: 11,
        completionTokens: 7,
        totalTokens: 18,
      },
      rawResponse: {
        model: "gpt-4o-mini-2026-03-01",
        choices: [{ message: { content: "Chat reply" } }],
        usage: {
          prompt_tokens: 11,
          completion_tokens: 7,
          total_tokens: 18,
        },
      },
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.openai.com/v1/chat/completions",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          authorization: "Bearer test-key",
          "content-type": "application/json",
        }),
        body: JSON.stringify({
          model: "gpt-4o-mini",
          messages: [{ role: "user", content: "hello" }],
          stream: false,
        }),
      }),
    );
  });

  it("translates an image generation request to the upstream images endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          created: 1_710_000_000,
          data: [{ url: "https://cdn.example.com/generated.png" }],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new OpenAIProviderClient({
      baseUrl: "https://api.openai.com/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(
      client.generateImage?.({
        model: "gpt-image-1",
        prompt: "a lighthouse in fog",
        n: 1,
        size: "1024x1024",
        responseFormat: "url",
        user: "user-123",
      }),
    ).resolves.toEqual({
      created: 1_710_000_000,
      data: [{ url: "https://cdn.example.com/generated.png" }],
      providerModel: "gpt-image-1",
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.openai.com/v1/images/generations",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          authorization: "Bearer test-key",
          "content-type": "application/json",
        }),
        body: JSON.stringify({
          model: "gpt-image-1",
          prompt: "a lighthouse in fog",
          n: 1,
          size: "1024x1024",
          response_format: "url",
          user: "user-123",
        }),
      }),
    );
  });

  it("uses model listing for readiness checks", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          data: [{ id: "gpt-image-1" }],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new OpenAIProviderClient({
      baseUrl: "https://api.openai.com/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("gpt-image-1")).resolves.toEqual({
      ready: true,
      detail: "startup model ready",
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.openai.com/v1/models",
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({
          authorization: "Bearer test-key",
        }),
      }),
    );
  });
});
