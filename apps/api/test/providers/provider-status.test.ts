import { describe, expect, it, vi } from "vitest";
import { ProviderRegistry } from "../../src/providers/registry";
import type { ProviderClient } from "../../src/providers/types";

describe("provider status", () => {
  it("returns health summary for all providers", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => false,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: false, healthy: false, detail: "missing key" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "fallback" })),
    };

    const registry = new ProviderRegistry({
      clients: [ollama, groq, mock],
      defaultProvider: "mock",
      modelProviderMap: { "fast-chat": "ollama" },
      providerModelMap: { ollama: "llama3.1:8b", groq: "llama-3.1-8b-instant", mock: "mock-chat" },
      fallbackOrder: { ollama: ["groq", "mock"], groq: ["ollama", "mock"], mock: [] },
    });

    const status = await registry.status();
    expect(status.providers).toHaveLength(3);
    expect(status.providers.find((provider) => provider.name === "ollama")?.healthy).toBe(true);
    expect(status.providers.find((provider) => provider.name === "groq")?.enabled).toBe(false);
  });
});
