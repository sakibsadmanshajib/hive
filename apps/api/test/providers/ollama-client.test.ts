import { afterEach, describe, expect, it, vi } from "vitest";
import { OllamaProviderClient } from "../../src/providers/ollama-client";

describe("ollama provider client readiness", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("reports ready when the configured model is installed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          models: [{ name: "llama3.1:8b" }, { name: "mistral:latest" }],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new OllamaProviderClient({
      baseUrl: "http://127.0.0.1:11434",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama3.1:8b")).resolves.toEqual({
      ready: true,
      detail: "startup model ready",
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:11434/api/tags",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("reports missing model when the configured model is absent", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          models: [{ name: "mistral:latest" }],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new OllamaProviderClient({
      baseUrl: "http://127.0.0.1:11434",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama3.1:8b")).resolves.toEqual({
      ready: false,
      detail: "startup model missing: llama3.1:8b",
    });
  });

  it("reports unreachable when the tags request fails", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("connect ECONNREFUSED"));
    vi.stubGlobal("fetch", fetchMock);

    const client = new OllamaProviderClient({
      baseUrl: "http://127.0.0.1:11434",
      timeoutMs: 50,
      maxRetries: 0,
    });

    await expect(client.checkModelReadiness("llama3.1:8b")).resolves.toEqual({
      ready: false,
      detail: "startup unreachable: ollama request failed: connect ECONNREFUSED",
    });
  });
});
