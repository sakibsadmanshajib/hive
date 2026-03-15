import { afterEach, describe, expect, it, vi } from "vitest";
import { AnthropicProviderClient } from "../../src/providers/anthropic-client";

describe("anthropic provider client chat support", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("translates chat requests to the native messages endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          model: "claude-sonnet-4-5-20260301",
          content: [
            {
              type: "text",
              text: "Anthropic reply",
            },
          ],
          usage: {
            input_tokens: 13,
            output_tokens: 9,
          },
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new AnthropicProviderClient({
      baseUrl: "https://api.anthropic.com/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(
      client.chat({
        model: "claude-sonnet-4-5",
        messages: [{ role: "user", content: "hello" }],
      }),
    ).resolves.toEqual({
      content: "Anthropic reply",
      providerModel: "claude-sonnet-4-5-20260301",
      usage: {
        promptTokens: 13,
        completionTokens: 9,
        totalTokens: 22,
      },
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.anthropic.com/v1/messages",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "x-api-key": "test-key",
          "anthropic-version": expect.any(String),
          "content-type": "application/json",
        }),
      }),
    );
  });
});
