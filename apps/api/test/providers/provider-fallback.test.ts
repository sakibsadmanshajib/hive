import { describe, expect, it, vi } from "vitest";
import { ProviderRegistry } from "../../src/providers/registry";
import type { ProviderClient } from "../../src/providers/types";

describe("provider fallback behavior", () => {
  it("uses configured fallback client when primary provider fails", async () => {
    const ollamaClient: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => {
        throw new Error("ollama unavailable");
      }),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };
    const groqClient: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "groq verified" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };
    const mockClient: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock response" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };

    const registry = new ProviderRegistry({
      clients: [ollamaClient, groqClient, mockClient],
      defaultProvider: "mock",
      modelProviderMap: { "fast-chat": "ollama" },
      providerModelMap: { ollama: "llama3.1:8b", groq: "llama-3.1-8b-instant", mock: "mock-chat" },
      fallbackOrder: {
        ollama: ["groq", "mock"],
        groq: ["mock"],
        mock: [],
      },
    });

    const result = await registry.chat("fast-chat", [{ role: "user", content: "hello" }]);

    expect(result.providerUsed).toBe("groq");
    expect(ollamaClient.chat).toHaveBeenCalledTimes(1);
    expect(groqClient.chat).toHaveBeenCalledTimes(1);
  });

  it("skips unavailable clients and fails after all candidates are exhausted", async () => {
    const ollamaClient: ProviderClient = {
      name: "ollama",
      isEnabled: vi.fn(() => false),
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: false, healthy: false, detail: "disabled" })),
    };
    const groqClient: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => {
        throw new Error("groq timed out");
      }),
      status: vi.fn(async () => ({ enabled: true, healthy: false, detail: "timeout" })),
    };
    const mockClient: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => {
        throw new Error("mock timed out");
      }),
      status: vi.fn(async () => ({ enabled: true, healthy: false, detail: "timeout" })),
    };

    const registry = new ProviderRegistry({
      clients: [ollamaClient, groqClient, mockClient],
      defaultProvider: "mock",
      modelProviderMap: { "fast-chat": "ollama" },
      providerModelMap: { ollama: "llama3.1:8b", groq: "llama-3.1-8b-instant", mock: "mock-chat" },
      fallbackOrder: {
        ollama: ["groq", "mock"],
        groq: ["mock"],
        mock: [],
      },
    });

    await expect(registry.chat("fast-chat", [{ role: "user", content: "hello" }])).rejects.toThrowError(
      /no provider succeeded/,
    );
    expect(ollamaClient.chat).not.toHaveBeenCalled();
    expect(groqClient.chat).toHaveBeenCalledTimes(1);
    expect(mockClient.chat).toHaveBeenCalledTimes(1);
  });
});
