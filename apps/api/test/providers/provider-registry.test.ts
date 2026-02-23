import { describe, expect, it, vi } from "vitest";
import { ProviderRegistry } from "../../src/providers/registry";
import type { ProviderClient } from "../../src/providers/types";

function createRegistry(clients: ProviderClient[]) {
  return new ProviderRegistry({
    clients,
    defaultProvider: "mock",
    modelProviderMap: {
      "fast-chat": "ollama",
      "smart-reasoning": "groq",
    },
    providerModelMap: {
      ollama: "llama3.1:8b",
      groq: "llama-3.1-8b-instant",
      mock: "mock-chat",
    },
    fallbackOrder: {
      ollama: ["groq", "mock"],
      groq: ["ollama", "mock"],
      mock: [],
    },
  });
}

describe("provider registry", () => {
  it("routes fast-chat to ollama first", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ollama ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "groq ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };

    const registry = createRegistry([ollama, groq, mock]);
    const result = await registry.chat("fast-chat", [{ role: "user", content: "hello" }]);

    expect(result.providerUsed).toBe("ollama");
    expect(ollama.chat).toHaveBeenCalledTimes(1);
    expect(groq.chat).not.toHaveBeenCalled();
  });
});
