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
      "image-basic": "mock",
    },
    providerModelMap: {
      ollama: "llama3.1:8b",
      groq: "llama-3.1-8b-instant",
      mock: "mock-image",
    },
    providerReadinessModels: {
      ollama: ["llama3.1:8b"],
      groq: ["llama-3.1-8b-instant"],
      mock: ["mock-image"],
    },
    fallbackOrder: {
      ollama: ["groq", "mock"],
      groq: ["ollama", "mock"],
      mock: [],
    },
  });
}

describe("provider registry", () => {
  it("routes embeddings through the upstream provider model while preserving the public model id", async () => {
    const embeddings = vi.fn(async () => ({
      data: [{ embedding: [0.1, 0.2, 0.3], index: 0 }],
      model: "text-embedding-3-small",
      providerModel: "openai/text-embedding-3-small",
      usage: { promptTokens: 3, totalTokens: 3 },
    }));
    const mock: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      embeddings,
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [mock],
      defaultProvider: "mock",
      modelProviderMap: {
        "text-embedding-3-small": "mock",
      },
      providerModelMap: {
        mock: "mock-image",
      },
      fallbackOrder: {
        mock: [],
      },
    });

    const result = await registry.embeddings("text-embedding-3-small", { input: "hello world" });

    expect(embeddings).toHaveBeenCalledWith({
      input: "hello world",
      model: "openai/text-embedding-3-small",
    });
    expect(result.body).toMatchObject({
      model: "text-embedding-3-small",
    });
    expect(result.headers["x-model-routed"]).toBe("text-embedding-3-small");
    expect(result.headers["x-provider-model"]).toBe("openai/text-embedding-3-small");
    expect(result.providerModel).toBe("openai/text-embedding-3-small");
  });

  it("passes through the requested embedding model id when no explicit upstream alias exists", async () => {
    const embeddings = vi.fn(async () => ({
      data: [{ embedding: [0.4, 0.5, 0.6], index: 0 }],
      model: "nvidia/llama-nemotron-embed-vl-1b-v2:free",
      providerModel: "nvidia/llama-nemotron-embed-vl-1b-v2:free",
      usage: { promptTokens: 2, totalTokens: 2 },
    }));
    const openrouter: ProviderClient = {
      name: "openrouter" as never,
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "mock ok" })),
      embeddings,
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [openrouter],
      defaultProvider: "openrouter" as never,
      modelProviderMap: {
        "nvidia/llama-nemotron-embed-vl-1b-v2:free": "openrouter" as never,
      },
      providerModelMap: {
        openrouter: "openrouter/auto",
      } as never,
      fallbackOrder: {
        openrouter: [],
      } as never,
    });

    const result = await registry.embeddings("nvidia/llama-nemotron-embed-vl-1b-v2:free", {
      input: "verification probe",
    });

    expect(embeddings).toHaveBeenCalledWith({
      input: "verification probe",
      model: "nvidia/llama-nemotron-embed-vl-1b-v2:free",
    });
    expect(result.body).toMatchObject({
      model: "nvidia/llama-nemotron-embed-vl-1b-v2:free",
    });
    expect(result.headers["x-provider-model"]).toBe("nvidia/llama-nemotron-embed-vl-1b-v2:free");
  });

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

  it("does not fall back from guest-free zero-cost routing into paid providers", async () => {
    const openrouter: ProviderClient = {
      name: "openrouter" as never,
      isEnabled: () => true,
      chat: vi.fn(async () => {
        throw new Error("free offer exhausted");
      }),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };
    const groq: ProviderClient = {
      name: "groq",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "paid fallback reply" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [openrouter, groq],
      defaultProvider: "openrouter" as never,
      modelProviderMap: {},
      modelOfferMap: {
        "guest-free": ["zero-free"],
      },
      modelOfferPolicyMap: {
        "guest-free": {
          allowedCostClasses: ["zero"],
        },
      },
      offerCatalog: {
        "zero-free": {
          provider: "openrouter" as never,
          upstreamModel: "openrouter/free-model",
          costClass: "zero",
        },
      },
      providerModelMap: {
        openrouter: "openrouter/free-model",
        groq: "llama-3.1-8b-instant",
      } as never,
      providerReadinessModels: {
        openrouter: ["openrouter/free-model"],
        groq: ["llama-3.1-8b-instant"],
      } as never,
      fallbackOrder: {
        openrouter: ["groq"] as never,
        groq: [],
      } as never,
    });

    await expect(
      registry.chat("guest-free", [{ role: "user", content: "hello" }]),
    ).rejects.toThrow(/free offer exhausted|no provider succeeded/);
    expect(groq.chat).not.toHaveBeenCalled();
  });

  it("rejects non-zero-cost offers for guest-free before provider dispatch", async () => {
    const openrouter: ProviderClient = {
      name: "openrouter" as never,
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "should not be called" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [openrouter],
      defaultProvider: "openrouter" as never,
      modelProviderMap: {},
      modelOfferMap: {
        "guest-free": ["misconfigured-offer"],
      },
      modelOfferPolicyMap: {
        "guest-free": {
          allowedCostClasses: ["zero"],
        },
      },
      offerCatalog: {
        "misconfigured-offer": {
          provider: "openrouter" as never,
          upstreamModel: "openrouter/paid-model",
          costClass: "fixed",
        },
      },
      providerModelMap: {
        openrouter: "openrouter/paid-model",
      } as never,
      providerReadinessModels: {
        openrouter: ["openrouter/paid-model"],
      } as never,
      fallbackOrder: {
        openrouter: [],
      } as never,
    });

    await expect(
      registry.chat("guest-free", [{ role: "user", content: "hello" }]),
    ).rejects.toThrow(/cost class fixed not allowed/i);
    expect(openrouter.chat).not.toHaveBeenCalled();
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

  it("checks free-offer readiness in addition to the provider default model", async () => {
    const openrouterCheck = vi.fn(async (model: string) => ({
      ready: true,
      detail: `startup model ready: ${model}`,
    }));
    const openrouter: ProviderClient = {
      name: "openrouter" as never,
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "reachable" })),
      checkModelReadiness: openrouterCheck,
    };

    const registry = new ProviderRegistry({
      clients: [openrouter],
      defaultProvider: "openrouter" as never,
      modelProviderMap: {},
      modelOfferMap: {
        "guest-free": ["openrouter-free"],
      },
      modelOfferPolicyMap: {
        "guest-free": {
          allowedCostClasses: ["zero"],
        },
      },
      offerCatalog: {
        "openrouter-free": {
          provider: "openrouter" as never,
          upstreamModel: "openrouter/free-model",
          costClass: "zero",
        },
      },
      providerModelMap: {
        openrouter: "openrouter/default-model",
      } as never,
      providerReadinessModels: {
        openrouter: ["openrouter/default-model", "openrouter/free-model"],
      } as never,
      fallbackOrder: {
        openrouter: [],
      } as never,
    });

    await registry.captureStartupReadiness();

    expect(openrouterCheck).toHaveBeenCalledTimes(2);
    expect(openrouterCheck).toHaveBeenNthCalledWith(1, "openrouter/default-model");
    expect(openrouterCheck).toHaveBeenNthCalledWith(2, "openrouter/free-model");
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

  it("fails cleanly when no provider supports image generation for the selected model", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ollama ok" })),
      generateImage: vi.fn(async () => {
        throw new Error("ollama timed out");
      }),
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

    await expect(
      registry.imageGeneration("image-basic", {
        prompt: "city skyline",
        n: 1,
        responseFormat: "url",
        size: "1024x1024",
      }),
    ).rejects.toThrow(/no provider succeeded|unsupported image capability/);
  });

  it("routes image generation through the configured provider fallback chain", async () => {
    const ollama: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "ollama ok" })),
      generateImage: vi.fn(async () => {
        throw new Error("ollama timed out");
      }),
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
      generateImage: vi.fn(async () => ({
        created: 1_710_000_000,
        data: [{ url: "https://cdn.example.com/image.png" }],
        providerModel: "mock-image",
      })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
      checkModelReadiness: vi.fn(async () => ({ ready: true, detail: "startup model ready" })),
    };

    const registry = new ProviderRegistry({
      clients: [ollama, groq, mock],
      defaultProvider: "mock",
      modelProviderMap: {
        "fast-chat": "ollama",
        "smart-reasoning": "groq",
        "image-basic": "ollama",
      },
      providerModelMap: {
        ollama: "image-primary",
        groq: "llama-3.1-8b-instant",
        mock: "mock-image",
      },
      fallbackOrder: {
        ollama: ["mock"],
        groq: ["mock"],
        mock: [],
      },
    });

    const result = await registry.imageGeneration("image-basic", {
      prompt: "city skyline",
      n: 1,
      responseFormat: "url",
      size: "1024x1024",
    });

    expect(result).toEqual({
      created: 1_710_000_000,
      data: [{ url: "https://cdn.example.com/image.png" }],
      providerUsed: "mock",
      providerModel: "mock-image",
    });
    expect(ollama.generateImage).toHaveBeenCalledTimes(1);
    expect(mock.generateImage).toHaveBeenCalledTimes(1);

    const metrics = await registry.metrics();
    const ollamaMetrics = metrics.providers.find((provider) => provider.name === "ollama");
    const mockMetrics = metrics.providers.find((provider) => provider.name === "mock");
    expect(ollamaMetrics).toMatchObject({
      name: "ollama",
      requests: 1,
      errors: 1,
      enabled: true,
      healthy: true,
    });
    expect(mockMetrics).toMatchObject({
      name: "mock",
      requests: 1,
      errors: 0,
      enabled: true,
      healthy: true,
    });
  });
});
