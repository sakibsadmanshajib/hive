import { CircuitBreaker, type CircuitBreakerConfig, type CircuitState } from "./circuit-breaker";
import type { ProviderChatMessage, ProviderClient, ProviderName } from "./types";

type ProviderRegistryConfig = {
  clients: ProviderClient[];
  defaultProvider: ProviderName;
  modelProviderMap: Record<string, ProviderName>;
  providerModelMap: Record<ProviderName, string>;
  fallbackOrder: Record<ProviderName, ProviderName[]>;
  circuitBreaker?: CircuitBreakerConfig;
};

export type ProviderExecutionResult = {
  content: string;
  providerUsed: ProviderName;
  providerModel: string;
};

export type ProviderStatusResult = {
  providers: Array<{
    name: ProviderName;
    enabled: boolean;
    healthy: boolean;
    detail: string;
    circuit: {
      state: CircuitState;
      failures: number;
    };
  }>;
};

export class ProviderRegistry {
  private readonly clientsByName: Map<ProviderName, ProviderClient>;
  private readonly circuitBreakers: Map<ProviderName, CircuitBreaker>;

  constructor(private readonly config: ProviderRegistryConfig) {
    this.clientsByName = new Map(config.clients.map((client) => [client.name, client]));
    this.circuitBreakers = new Map(
      config.clients.map((client) => [
        client.name,
        new CircuitBreaker(client.name, config.circuitBreaker ?? { failureThreshold: 5, resetTimeoutMs: 30000 }),
      ]),
    );
  }

  async chat(modelId: string, messages: ProviderChatMessage[]): Promise<ProviderExecutionResult> {
    const primaryProvider = this.config.modelProviderMap[modelId] ?? this.config.defaultProvider;
    const candidateProviders = this.buildCandidates(primaryProvider);
    const errors: string[] = [];

    for (const providerName of candidateProviders) {
      const client = this.clientsByName.get(providerName);
      const breaker = this.circuitBreakers.get(providerName);

      if (!client || !client.isEnabled()) {
        continue;
      }

      if (breaker) {
        breaker.evaluateState();
        if (breaker.isOpen()) {
          errors.push(`${providerName}: circuit open`);
          continue;
        }
        if (breaker.isHalfOpen() && !breaker.tryAcquireProbe()) {
          errors.push(`${providerName}: half-open probe in-flight`);
          continue;
        }
      }

      const providerModel = this.config.providerModelMap[providerName] ?? modelId;
      try {
        const response = await client.chat({ model: providerModel, messages });
        breaker?.recordSuccess();
        return {
          content: response.content,
          providerUsed: providerName,
          providerModel: response.providerModel ?? providerModel,
        };
      } catch (error) {
        breaker?.recordFailure();
        const reason = error instanceof Error ? error.message : String(error);
        errors.push(`${providerName}: ${reason}`);
      }
    }

    throw new Error(`no provider succeeded${errors.length > 0 ? ` (${errors.join(" | ")})` : ""}`);
  }

  async status(): Promise<ProviderStatusResult> {
    const providers: ProviderStatusResult["providers"] = [];
    for (const providerName of ["ollama", "groq", "mock"] as const) {
      const client = this.clientsByName.get(providerName);
      const breaker = this.circuitBreakers.get(providerName);

      if (!client) {
        providers.push({
          name: providerName,
          enabled: false,
          healthy: false,
          detail: "not registered",
          circuit: { state: "CLOSED", failures: 0 },
        });
        continue;
      }
      const health = await client.status();
      providers.push({
        name: providerName,
        enabled: health.enabled,
        healthy: health.healthy,
        detail: health.detail,
        circuit: {
          state: breaker?.getState() ?? "CLOSED",
          failures: breaker?.getFailures() ?? 0,
        },
      });
    }
    return { providers };
  }

  private buildCandidates(primary: ProviderName): ProviderName[] {
    const ordered = [primary, ...(this.config.fallbackOrder[primary] ?? [])];
    return [...new Set(ordered)];
  }
}
