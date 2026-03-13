import { CircuitBreaker, type CircuitBreakerConfig, type CircuitState } from "./circuit-breaker";
import { ProviderMetrics } from "./provider-metrics";
import type {
  ProviderChatMessage,
  ProviderClient,
  ProviderImageRequest,
  ProviderMetricsResult,
  ProviderName,
  ProviderReadinessStatus,
} from "./types";

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

export type ProviderImageExecutionResult = {
  created: number;
  data: Array<{
    url?: string;
    b64Json?: string;
  }>;
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
      lastError?: string;
    };
  }>;
};

const ALL_PROVIDERS = ["ollama", "groq", "openai", "mock"] as const;
const METRICS_STATUS_CACHE_TTL_MS = 5000;

/**
 * ProviderRegistry manages multiple AI provider clients, handling model routing,
 * failover logic, and circuit breaking for resilience.
 */
export class ProviderRegistry {
  private readonly clientsByName: Map<ProviderName, ProviderClient>;
  private readonly circuitBreakers: Map<ProviderName, CircuitBreaker>;
  private readonly metricsCollector = new ProviderMetrics(ALL_PROVIDERS);
  private readonly startupReadiness = new Map<ProviderName, ProviderReadinessStatus>();
  private cachedMetricsStatus?: ProviderStatusResult;
  private cachedMetricsStatusAt = 0;

  constructor(private readonly config: ProviderRegistryConfig) {
    this.clientsByName = new Map(config.clients.map((client) => [client.name, client]));
    this.circuitBreakers = new Map(
      config.clients.map((client) => [
        client.name,
        new CircuitBreaker(client.name, config.circuitBreaker ?? { failureThreshold: 5, resetTimeoutMs: 30000 }),
      ]),
    );
  }

  /**
   * Executes a chat completion request using the primary provider for the model,
   * falling back to secondary providers if failures occur.
   *
   * @param modelId - The internal model ID to route.
   * @param messages - The chat messages to process.
   * @throws Error if no provider succeeds or if all candidates are blocked by circuit breakers.
   */
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
      const startedAt = Date.now();
      try {
        const response = await client.chat({ model: providerModel, messages });
        this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, false);
        breaker?.recordSuccess();
        return {
          content: response.content,
          providerUsed: providerName,
          providerModel: response.providerModel ?? providerModel,
        };
      } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, true);
        breaker?.recordFailure(reason);
        errors.push(`${providerName}: ${reason}`);
      }
    }

    throw new Error(`no provider succeeded${errors.length > 0 ? ` (${errors.join(" | ")})` : ""}`);
  }

  async imageGeneration(modelId: string, request: Omit<ProviderImageRequest, "model">): Promise<ProviderImageExecutionResult> {
    const primaryProvider = this.config.modelProviderMap[modelId] ?? this.config.defaultProvider;
    const candidateProviders = this.buildCandidates(primaryProvider);
    const errors: string[] = [];

    for (const providerName of candidateProviders) {
      const client = this.clientsByName.get(providerName);
      const breaker = this.circuitBreakers.get(providerName);

      if (!client || !client.isEnabled()) {
        continue;
      }

      if (!client.generateImage) {
        errors.push(`${providerName}: unsupported image capability`);
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
      const startedAt = Date.now();
      try {
        const response = await client.generateImage({ ...request, model: providerModel });
        this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, false);
        breaker?.recordSuccess();
        return {
          created: response.created,
          data: response.data,
          providerUsed: providerName,
          providerModel: response.providerModel ?? providerModel,
        };
      } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, true);
        breaker?.recordFailure(reason);
        errors.push(`${providerName}: ${reason}`);
      }
    }

    throw new Error(`no provider succeeded${errors.length > 0 ? ` (${errors.join(" | ")})` : ""}`);
  }

  /**
   * Returns the current status of all registered providers, including health and circuit state.
   */
  async status(): Promise<ProviderStatusResult> {
    const providers: ProviderStatusResult["providers"] = [];
    for (const providerName of ALL_PROVIDERS) {
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
      const readiness = this.startupReadiness.get(providerName);
      providers.push({
        name: providerName,
        enabled: health.enabled,
        healthy: health.healthy,
        detail: readiness ? `${health.detail}; ${readiness.detail}` : health.detail,
        circuit: {
          state: breaker?.getState() ?? "CLOSED",
          failures: breaker?.getFailures() ?? 0,
          lastError: breaker?.getLastError(),
        },
      });
    }
    return { providers };
  }

  async metrics(): Promise<ProviderMetricsResult> {
    const status = await this.getCachedMetricsStatus();
    return this.metricsCollector.summarize(status.providers);
  }

  async metricsPrometheus(): Promise<{ contentType: string; body: string }> {
    const status = await this.getCachedMetricsStatus();
    return this.metricsCollector.renderPrometheus(status.providers);
  }

  async captureStartupReadiness(): Promise<Record<ProviderName, ProviderReadinessStatus>> {
    const results = {} as Record<ProviderName, ProviderReadinessStatus>;

    for (const providerName of ALL_PROVIDERS) {
      const client = this.clientsByName.get(providerName);
      if (!client) {
        const missing = { ready: false, detail: "not registered" };
        this.startupReadiness.set(providerName, missing);
        results[providerName] = missing;
        continue;
      }

      let readiness: ProviderReadinessStatus;
      try {
        const providerModel = this.config.providerModelMap[providerName];
        readiness = await client.checkModelReadiness(providerModel);
      } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        readiness = { ready: false, detail: `startup readiness failed: ${reason}` };
      }
      this.startupReadiness.set(providerName, readiness);
      results[providerName] = readiness;
    }

    this.cachedMetricsStatus = undefined;
    this.cachedMetricsStatusAt = 0;

    return results;
  }

  private buildCandidates(primary: ProviderName): ProviderName[] {
    const ordered = [primary, ...(this.config.fallbackOrder[primary] ?? [])];
    return [...new Set(ordered)];
  }

  private async getCachedMetricsStatus(): Promise<ProviderStatusResult> {
    const now = Date.now();
    if (this.cachedMetricsStatus && now - this.cachedMetricsStatusAt < METRICS_STATUS_CACHE_TTL_MS) {
      return this.cachedMetricsStatus;
    }

    const status = await this.status();
    this.cachedMetricsStatus = status;
    this.cachedMetricsStatusAt = now;
    return status;
  }
}
