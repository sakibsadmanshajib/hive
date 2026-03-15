import { describe, expect, it, vi } from "vitest";
import { ProviderRegistry } from "../../src/providers/registry";
import type { ProviderClient, ProviderName } from "../../src/providers/types";

describe("provider status", () => {
  it("returns health summary for all providers", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => false,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: false, healthy: false, detail: "missing key" })),
      checkModelReadiness: vi.fn(async () => ({ ready: false, detail: "disabled by config" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "fallback" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [ollama, groq, mock],
      defaultProvider: "mock",
      modelProviderMap: { "fast-chat": "ollama" },
      providerModelMap: { ollama: "llama3.1:8b", groq: "llama-3.1-8b-instant", mock: "mock-chat" },
      fallbackOrder: { ollama: ["groq", "mock"], groq: ["ollama", "mock"], mock: [] },
    });

    const status = await registry.status();
    expect(status.providers).toHaveLength(7);
    expect(status.providers.find((provider) => provider.name === "ollama")?.healthy).toBe(true);
    expect(status.providers.find((provider) => provider.name === "groq")?.enabled).toBe(false);
    expect(status.providers.find((provider) => provider.name === "openai")).toMatchObject({
      enabled: false,
      healthy: false,
      detail: "not registered",
    });
    expect(status.providers.find((provider) => provider.name === "openrouter")).toMatchObject({
      enabled: false,
      healthy: false,
      detail: "not registered",
    });
  });

  it("enriches internal detail with startup readiness state", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({ ready: false, detail: "startup model missing: llama3.1:8b" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => false,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: false, healthy: false, detail: "missing key" })),
      checkModelReadiness: vi.fn(async () => ({ ready: false, detail: "disabled by config" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "fallback" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [ollama, groq, mock],
      defaultProvider: "mock",
      modelProviderMap: { "fast-chat": "ollama" },
      providerModelMap: { ollama: "llama3.1:8b", groq: "llama-3.1-8b-instant", mock: "mock-chat" },
      fallbackOrder: { ollama: ["groq", "mock"], groq: ["ollama", "mock"], mock: [] },
    });

    await registry.captureStartupReadiness();

    const status = await registry.status();
    expect(status.providers.find((provider) => provider.name === "ollama")).toMatchObject({
      detail: "reachable; startup model missing: llama3.1:8b",
    });
    expect(status.providers.find((provider) => provider.name === "groq")).toMatchObject({
      detail: "missing key; disabled by config",
    });
  });

  it("includes provider health snapshots in metrics summaries", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => false,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: false, healthy: false, detail: "missing key" })),
      checkModelReadiness: vi.fn(async () => ({ ready: false, detail: "disabled by config" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "fallback" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [ollama, groq, mock],
      defaultProvider: "mock",
      modelProviderMap: { "fast-chat": "ollama" },
      providerModelMap: { ollama: "llama3.1:8b", groq: "llama-3.1-8b-instant", mock: "mock-chat" },
      fallbackOrder: { ollama: ["groq", "mock"], groq: ["ollama", "mock"], mock: [] },
    });

    const metrics = await registry.metrics();

    expect(metrics.providers.find((provider) => provider.name === "ollama")).toMatchObject({
      enabled: true,
      healthy: true,
      detail: "reachable",
    });
    expect(metrics.providers.find((provider) => provider.name === "groq")).toMatchObject({
      enabled: false,
      healthy: false,
      detail: "missing key",
    });
  });

  it("surfaces openrouter, gemini, and anthropic in provider status summaries", async () => {
    const openrouter: ProviderClient = {
      name: "openrouter",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const gemini: ProviderClient = {
      name: "gemini",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const anthropic: ProviderClient = {
      name: "anthropic",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const providerModelMap: Record<ProviderName, string> = {
      anthropic: "claude-sonnet-4-5",
      gemini: "gemini-2.5-flash",
      groq: "llama-3.1-8b-instant",
      mock: "mock-chat",
      ollama: "llama3.1:8b",
      openai: "gpt-4o-mini",
      openrouter: "openrouter/free-model",
    };
    const fallbackOrder: Record<ProviderName, ProviderName[]> = {
      anthropic: [],
      gemini: [],
      groq: [],
      mock: [],
      ollama: [],
      openai: [],
      openrouter: [],
    };

    const registry = new ProviderRegistry({
      clients: [openrouter, gemini, anthropic],
      defaultProvider: "openrouter",
      modelProviderMap: {
        "guest-free": "openrouter",
      },
      providerModelMap,
      fallbackOrder,
    });

    const status = await registry.status();

    expect(status.providers.find((provider) => provider.name === "openrouter")).toMatchObject({
      enabled: true,
      healthy: true,
      detail: "reachable",
    });
    expect(status.providers.find((provider) => provider.name === "gemini")).toMatchObject({
      enabled: true,
      healthy: true,
      detail: "reachable",
    });
    expect(status.providers.find((provider) => provider.name === "anthropic")).toMatchObject({
      enabled: true,
      healthy: true,
      detail: "reachable",
    });
  });
});
