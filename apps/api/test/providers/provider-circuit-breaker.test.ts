import { describe, expect, it, vi, afterEach } from "vitest";
import { ProviderRegistry } from "../../src/providers/registry";
import type { ProviderClient } from "../../src/providers/types";

describe("provider circuit breaker", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("trips the circuit after repeated failures and skips the provider", async () => {
    const failingClient: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn(async () => {
        throw new Error("connection refused");
      }),
      status: vi.fn(async () => ({ enabled: true, healthy: false, detail: "error" })),
    };
    const backupClient: ProviderClient = {
      name: "mock",
      isEnabled: () => true,
      chat: vi.fn(async () => ({ content: "backup ok" })),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };

    const registry = new ProviderRegistry({
      clients: [failingClient, backupClient],
      defaultProvider: "mock",
      modelProviderMap: { "test-model": "ollama" },
      providerModelMap: { ollama: "llama", mock: "mock" },
      fallbackOrder: { ollama: ["mock"], mock: [] },
      circuitBreaker: { failureThreshold: 2, resetTimeoutMs: 1000 },
    });

    // 1st failure
    await registry.chat("test-model", []);
    expect(failingClient.chat).toHaveBeenCalledTimes(1);

    // 2nd failure - should trip
    await registry.chat("test-model", []);
    expect(failingClient.chat).toHaveBeenCalledTimes(2);

    // 3rd call - circuit should be OPEN, skip failingClient
    const result = await registry.chat("test-model", []);
    expect(result.providerUsed).toBe("mock");
    expect(failingClient.chat).toHaveBeenCalledTimes(2); // still 2

    const status = await registry.status();
    const ollamaStatus = status.providers.find(p => p.name === "ollama");
    expect(ollamaStatus?.circuit.state).toBe("OPEN");
    expect(ollamaStatus?.circuit.failures).toBe(2);
  });

  it("recovers after reset timeout (HALF_OPEN -> CLOSED)", async () => {
    // Mock Date.now to control time
    let now = 1000000;
    vi.spyOn(Date, "now").mockImplementation(() => now);

    const client: ProviderClient = {
      name: "ollama",
      isEnabled: () => true,
      chat: vi.fn()
        .mockRejectedValueOnce(new Error("fail"))
        .mockResolvedValueOnce({ content: "recovered" }),
      status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
    };

    const registry = new ProviderRegistry({
      clients: [client],
      defaultProvider: "ollama",
      modelProviderMap: {},
      providerModelMap: { ollama: "llama" },
      fallbackOrder: {},
      circuitBreaker: { failureThreshold: 1, resetTimeoutMs: 1000 },
    });

    // trip it
    await expect(registry.chat("any", [])).rejects.toThrow();
    expect(client.chat).toHaveBeenCalledTimes(1);

    // circuit is open
    await expect(registry.chat("any", [])).rejects.toThrow(/circuit open/);
    expect(client.chat).toHaveBeenCalledTimes(1);

    // Advance time past reset timeout
    now += 1001;

    // should be HALF_OPEN and allow one call
    const result = await registry.chat("any", []);
    expect(result.content).toBe("recovered");
    expect(client.chat).toHaveBeenCalledTimes(2);

    // should be CLOSED now
    const status = await registry.status();
    expect(status.providers.find(p => p.name === "ollama")?.circuit.state).toBe("CLOSED");
  });

  it("only allows one probe request in HALF_OPEN state", async () => {
    let now = 1000000;
    vi.spyOn(Date, "now").mockImplementation(() => now);

    let resolveProbe: (value: any) => void;
    const probePromise = new Promise((resolve) => {
      resolveProbe = resolve;
    });
    
          const client: ProviderClient = {
            name: "ollama",
            isEnabled: () => true,
            chat: vi.fn()
              .mockRejectedValueOnce(new Error("initial failure"))
              .mockReturnValueOnce(probePromise as any) // Use return value directly
              .mockResolvedValueOnce({ content: "probe success" }), // Post-recovery call
            status: vi.fn(async () => ({ enabled: true, healthy: true, detail: "ok" })),
          };
        const registry = new ProviderRegistry({
      clients: [client],
      defaultProvider: "ollama",
      modelProviderMap: {},
      providerModelMap: { ollama: "llama" },
      fallbackOrder: {},
      circuitBreaker: { failureThreshold: 1, resetTimeoutMs: 1000 },
    });

    // 1. Trip the circuit
    await expect(registry.chat("any", [])).rejects.toThrow();
    
    // 2. Advance time past reset timeout
    now += 1001;

    // 3. Start first probe (will hang)
    const call1 = registry.chat("any", []);
    
    // 4. Second concurrent call should be blocked immediately (half-open probe in-flight)
    await expect(registry.chat("any", [])).rejects.toThrow(/half-open probe in-flight/);
    
    expect(client.chat).toHaveBeenCalledTimes(2);

    // 5. Complete the probe
    resolveProbe!({ content: "probe success" });
    const result1 = await call1;
    expect(result1.content).toBe("probe success");

    // 6. Circuit should now be CLOSED and allow normal requests
    const result2 = await registry.chat("any", []);
    expect(result2.content).toBe("probe success");
    expect(client.chat).toHaveBeenCalledTimes(3);
  });
});
