import { CircuitBreaker, type CircuitBreakerConfig, type CircuitState } from "./circuit-breaker";
import { ProviderMetrics } from "./provider-metrics";
import type {
  ProviderChatMessage,
  ProviderChatResponse,
  ProviderClient,
  ProviderCostClass,
  ProviderEmbeddingsRequest,
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
  providerReadinessModels?: Record<ProviderName, string[]>;
  fallbackOrder: Record<ProviderName, ProviderName[]>;
  offerCatalog?: Record<string, {
    provider: ProviderName;
    upstreamModel: string;
    costClass: ProviderCostClass;
  }>;
  modelOfferMap?: Record<string, string[]>;
  modelOfferPolicyMap?: Record<string, {
    allowedCostClasses?: ProviderCostClass[];
  }>;
  circuitBreaker?: CircuitBreakerConfig;
};

export type ProviderExecutionResult = {
  content: string;
  providerUsed: ProviderName;
  providerModel: string;
  rawResponse?: ProviderChatResponse["rawResponse"];
};

export type ProviderImageExecutionResult = {
  created: number;
  data: Array<{
    url?: string;
    b64Json?: string;
    revisedPrompt?: string;
  }>;
  providerUsed: ProviderName;
  providerModel: string;
};

export type ProviderStreamExecutionResult = {
  response: Response;
  providerUsed: ProviderName;
  providerModel: string;
};

export type ProviderEmbeddingsExecutionResult = {
  statusCode: number;
  body: unknown;
  headers: Record<string, string>;
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

const ALL_PROVIDERS = ["ollama", "groq", "openai", "openrouter", "gemini", "anthropic", "mock"] as const;
const METRICS_STATUS_CACHE_TTL_MS = 5000;
const EMBEDDING_PROVIDER_MODEL_MAP: Record<string, string> = {
  "text-embedding-3-small": "openai/text-embedding-3-small",
};

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
  async chat(modelId: string, messages: ProviderChatMessage[], params?: Record<string, unknown>): Promise<ProviderExecutionResult> {
    const offerIds = this.config.modelOfferMap?.[modelId];
    if (offerIds) {
      return this.chatWithOffers(modelId, offerIds, messages, params);
    }

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
        const response = await client.chat({ model: providerModel, messages, params });
        this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, false);
        breaker?.recordSuccess();
        return {
          content: response.content,
          providerUsed: providerName,
          providerModel: response.providerModel ?? providerModel,
          rawResponse: response.rawResponse,
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

  async chatStream(
    modelId: string,
    messages: ProviderChatMessage[],
    params?: Record<string, unknown>,
  ): Promise<ProviderStreamExecutionResult> {
    const offerIds = this.config.modelOfferMap?.[modelId];
    let providerName: ProviderName;
    let providerModel: string;

    if (offerIds && offerIds.length > 0) {
      const offerId = offerIds[0];
      const offer = this.config.offerCatalog?.[offerId];
      if (!offer) throw new Error(`${offerId}: offer not configured`);
      providerName = offer.provider;
      providerModel = offer.upstreamModel;
    } else {
      providerName = this.config.modelProviderMap[modelId] ?? this.config.defaultProvider;
      providerModel = this.config.providerModelMap[providerName] ?? modelId;
    }

    const client = this.clientsByName.get(providerName);
    if (!client?.isEnabled() || !client.chatStream) {
      throw new Error(`${providerName}: streaming not supported`);
    }

    const breaker = this.circuitBreakers.get(providerName);
    if (breaker) {
      breaker.evaluateState();
      if (breaker.isOpen()) throw new Error(`${providerName}: circuit open`);
    }

    const startedAt = Date.now();
    try {
      const response = await client.chatStream({ model: providerModel, messages, params });
      this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, false);
      breaker?.recordSuccess();
      return { response, providerUsed: providerName, providerModel };
    } catch (error) {
      this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, true);
      breaker?.recordFailure(error instanceof Error ? error.message : String(error));
      throw error;
    }
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

  async embeddings(modelId: string, request: Omit<ProviderEmbeddingsRequest, "model">): Promise<ProviderEmbeddingsExecutionResult> {
    const primaryProvider = this.config.modelProviderMap[modelId] ?? this.config.defaultProvider;
    const candidateProviders = this.buildCandidates(primaryProvider);
    const errors: string[] = [];

    for (const providerName of candidateProviders) {
      const client = this.clientsByName.get(providerName);
      const breaker = this.circuitBreakers.get(providerName);

      if (!client || !client.isEnabled()) {
        continue;
      }

      if (!client.embeddings) {
        errors.push(`${providerName}: unsupported embeddings capability`);
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

      const providerModel = EMBEDDING_PROVIDER_MODEL_MAP[modelId]
        ?? this.config.providerModelMap[providerName]
        ?? modelId;
      const startedAt = Date.now();
      try {
        const result = await client.embeddings({ ...request, model: providerModel });
        this.metricsCollector.recordAttempt(providerName, Date.now() - startedAt, false);
        breaker?.recordSuccess();
        return {
          statusCode: 200,
          body: {
            object: "list",
            data: result.data.map((item, index) => ({
              object: "embedding",
              embedding: item.embedding,
              index,
            })),
            model: modelId,
            usage: {
              prompt_tokens: result.usage?.promptTokens ?? 0,
              total_tokens: result.usage?.totalTokens ?? 0,
            },
          },
          headers: {
            "x-model-routed": modelId,
            "x-provider-used": providerName,
            "x-provider-model": result.providerModel ?? providerModel,
          },
          providerUsed: providerName,
          providerModel: result.providerModel ?? providerModel,
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
        const providerModels = this.getProviderReadinessModels(providerName);
        readiness = await this.captureProviderReadiness(client, providerModels);
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

  private async chatWithOffers(
    modelId: string,
    offerIds: string[],
    messages: ProviderChatMessage[],
    params?: Record<string, unknown>,
  ): Promise<ProviderExecutionResult> {
    const errors: string[] = [];
    const allowedCostClasses = this.config.modelOfferPolicyMap?.[modelId]?.allowedCostClasses;

    for (const offerId of offerIds) {
      const offer = this.config.offerCatalog?.[offerId];
      if (!offer) {
        errors.push(`${offerId}: offer not configured`);
        continue;
      }
      if (allowedCostClasses && !allowedCostClasses.includes(offer.costClass)) {
        errors.push(`${offerId}: cost class ${offer.costClass} not allowed for ${modelId}`);
        continue;
      }

      const client = this.clientsByName.get(offer.provider);
      const breaker = this.circuitBreakers.get(offer.provider);

      if (!client || !client.isEnabled()) {
        errors.push(`${offer.provider}: unavailable`);
        continue;
      }

      if (breaker) {
        breaker.evaluateState();
        if (breaker.isOpen()) {
          errors.push(`${offer.provider}: circuit open`);
          continue;
        }
        if (breaker.isHalfOpen() && !breaker.tryAcquireProbe()) {
          errors.push(`${offer.provider}: half-open probe in-flight`);
          continue;
        }
      }

      const startedAt = Date.now();
      try {
        const response = await client.chat({ model: offer.upstreamModel, messages, params });
        this.metricsCollector.recordAttempt(offer.provider, Date.now() - startedAt, false);
        breaker?.recordSuccess();
        return {
          content: response.content,
          providerUsed: offer.provider,
          providerModel: response.providerModel ?? offer.upstreamModel,
          rawResponse: response.rawResponse,
        };
      } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        this.metricsCollector.recordAttempt(offer.provider, Date.now() - startedAt, true);
        breaker?.recordFailure(reason);
        errors.push(`${offer.provider}: ${reason}`);
      }
    }

    throw new Error(`no provider succeeded${errors.length > 0 ? ` (${errors.join(" | ")})` : ""}`);
  }

  private getProviderReadinessModels(providerName: ProviderName): string[] {
    const configured = this.config.providerReadinessModels?.[providerName];
    if (configured && configured.length > 0) {
      return [...new Set(configured)];
    }

    const fallback = this.config.providerModelMap[providerName];
    return fallback ? [fallback] : [];
  }

  private async captureProviderReadiness(
    client: ProviderClient,
    providerModels: string[],
  ): Promise<ProviderReadinessStatus> {
    if (providerModels.length === 0) {
      return { ready: false, detail: "no readiness models configured" };
    }

    const results: Array<{ model: string; readiness: ProviderReadinessStatus }> = [];
    for (const model of providerModels) {
      results.push({
        model,
        readiness: await client.checkModelReadiness(model),
      });
    }

    const failures = results.filter((entry) => !entry.readiness.ready);
    if (results.length === 1) {
      return results[0].readiness;
    }

    if (failures.length === 0) {
      return {
        ready: true,
        detail: `startup models ready: ${results.map((entry) => entry.model).join(", ")}`,
      };
    }

    const allDisabled = failures.length === results.length
      && failures.every((entry) => entry.readiness.detail === "disabled by config");
    if (allDisabled) {
      return { ready: false, detail: "disabled by config" };
    }

    return {
      ready: false,
      detail: failures.map((entry) => `${entry.model}: ${entry.readiness.detail}`).join("; "),
    };
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
