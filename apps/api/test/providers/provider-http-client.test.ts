import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchWithRetry } from "../../src/providers/http-client";

describe("provider http client", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("retries on retryable upstream status and succeeds", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("upstream down", { status: 503 }))
      .mockResolvedValueOnce(new Response("ok", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await fetchWithRetry({
      provider: "groq",
      url: "https://api.groq.com/openai/v1/chat/completions",
      init: { method: "POST" },
      timeoutMs: 50,
      maxRetries: 1,
    });

    expect(response.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does not retry on non-retryable status", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("bad request", { status: 400 }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await fetchWithRetry({
      provider: "groq",
      url: "https://api.groq.com/openai/v1/chat/completions",
      init: { method: "POST" },
      timeoutMs: 50,
      maxRetries: 3,
    });

    expect(response.status).toBe(400);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("throws after timeout retries are exhausted", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new DOMException("aborted", "AbortError"));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      fetchWithRetry({
        provider: "ollama",
        url: "http://127.0.0.1:11434/api/chat",
        init: { method: "POST" },
        timeoutMs: 5,
        maxRetries: 2,
      }),
    ).rejects.toThrowError(/timed out/);

    expect(fetchMock).toHaveBeenCalledTimes(3);
  });
});
