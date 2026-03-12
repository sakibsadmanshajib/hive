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
  it("persists startup readiness snapshots for internal status", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ollama ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({
        ready: false,
        detail: "startup model missing: llama3.1:8b",
      })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "groq ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({
        ready: true,
        detail: "startup model ready",
      })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "always available fallback" })),
      checkModelReadiness: vi.fn(async () => ({
        ready: true,
        detail: "startup model ready",
      })),
    };

    const registry = createRegistry([ollama, groq, mock]);

    await registry.captureStartupReadiness();

    const status = await registry.status();
    expect(status.providers.find((provider) => provider.name === "ollama")?.detail).toBe(
      "reachable; startup model missing: llama3.1:8b",
    );
    expect(status.providers.find((provider) => provider.name === "groq")?.detail).toBe(
      "reachable; startup model ready",
    );
  });

  it("routes fast-chat to ollama first", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ollama ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "groq ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = createRegistry([ollama, groq, mock]);
    const result = await registry.chat("fast-chat", [{ role: "user", content: "hello" }]);

    expect(result.providerUsed).toBe("ollama");
    expect(ollama.chat).toHaveBeenCalledTimes(1);
    expect(groq.chat).not.toHaveBeenCalled();
  });

  it("continues startup readiness capture after one provider readiness check throws", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ollama ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => {
        throw new Error("catalog exploded");
      }),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "groq ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: vi.fn(async () => ({
        ready: true,
        detail: "startup model ready",
      })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "always available fallback" })),
      checkModelReadiness: vi.fn(async () => ({
        ready: true,
        detail: "startup model ready",
      })),
    };

    const registry = createRegistry([ollama, groq, mock]);

    const readiness = await registry.captureStartupReadiness();

    expect(readiness.ollama).toEqual({
      ready: false,
      detail: "startup readiness failed: catalog exploded",
    });
    expect(readiness.groq).toEqual({
      ready: true,
      detail: "startup model ready",
    });
    expect(groq.checkModelReadiness).toHaveBeenCalledTimes(1);
    expect(mock.checkModelReadiness).toHaveBeenCalledTimes(1);
  });

  it("records provider-level request, error, and latency metrics across fallback attempts", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => {
        throw new Error("timeout");
      }),
      status: vi.fn(async () => ({ enabled: true, healthy: false, detail: "timeout" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "groq ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = createRegistry([ollama, groq, mock]);

    await registry.chat("fast-chat", [{ role: "user", content: "hello" }]);

    const metrics = await registry.metrics();
    const ollamaMetrics = metrics.providers.find((provider) => provider.name === "ollama");
    const groqMetrics = metrics.providers.find((provider) => provider.name === "groq");

    expect(ollamaMetrics).toMatchObject({
      name: "ollama",
      requests: 1,
      errors: 1,
      enabled: true,
      healthy: false,
    });
    expect(groqMetrics).toMatchObject({
      name: "groq",
      requests: 1,
      errors: 0,
      enabled: true,
      healthy: true,
    });
    expect(ollamaMetrics?.latencyMs.avg).toBeGreaterThanOrEqual(0);
    expect(groqMetrics?.latencyMs.avg).toBeGreaterThanOrEqual(0);
  });

  it("reuses a short-lived provider status snapshot across metrics scrapes", async () => {
    const ollamaStatus = vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" }));
    const groqStatus = vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" }));
    const mockStatus = vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" }));
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ollama ok" })),
      status: ollamaStatus,
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "groq ok" })),
      status: groqStatus,
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      status: mockStatus,
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = createRegistry([ollama, groq, mock]);

    await registry.metrics();
    await registry.metricsPrometheus();

    expect(ollamaStatus).toHaveBeenCalledTimes(1);
    expect(groqStatus).toHaveBeenCalledTimes(1);
    expect(mockStatus).toHaveBeenCalledTimes(1);
  });
});
