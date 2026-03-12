import { afterEach, describe, expect, it, vi } from "vitest";
import { GroqProviderClient } from "../../src/providers/groq-client";

describe("groq provider client readiness", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("reports ready when the configured model is listed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          data: [{ id: "llama-3.1-8b-instant" }, { id: "mixtral-8x7b" }],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new GroqProviderClient({
      baseUrl: "https://api.groq.com/openai/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama-3.1-8b-instant")).resolves.toEqual({
      ready: true,
      detail: "startup model ready",
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.groq.com/openai/v1/models",
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({ authorization: "Bearer test-key" }),
      }),
    );
  });

  it("reports missing model when the configured model is unavailable", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          data: [{ id: "mixtral-8x7b" }],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new GroqProviderClient({
      baseUrl: "https://api.groq.com/openai/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama-3.1-8b-instant")).resolves.toEqual({
      ready: false,
      detail: "startup model missing: llama-3.1-8b-instant",
    });
  });

  it("reports startup models check failure for non-ok responses", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("upstream error", { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);

    const client = new GroqProviderClient({
      baseUrl: "https://api.groq.com/openai/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama-3.1-8b-instant")).resolves.toEqual({
      ready: false,
      detail: "startup models check failed: 500",
    });
  });

  it("reports disabled by config when the provider is not enabled", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const client = new GroqProviderClient({
      baseUrl: "https://api.groq.com/openai/v1",
      apiKey: undefined,
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama-3.1-8b-instant")).resolves.toEqual({
      ready: false,
      detail: "disabled by config",
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("reports unreachable when the model catalog request fails", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("network down"));
    vi.stubGlobal("fetch", fetchMock);

    const client = new GroqProviderClient({
      baseUrl: "https://api.groq.com/openai/v1",
      apiKey: "test-key",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama-3.1-8b-instant")).resolves.toEqual({
      ready: false,
      detail: "startup unreachable: groq request failed: network down",
    });
  });
});
