import { Counter, Gauge, Histogram, Registry } from "prom-client";
import type { ProviderMetricsResult, ProviderMetricsSummary, ProviderName } from "./types";

type ProviderStatusSnapshot = Omit<ProviderMetricsSummary, "requests" | "errors" | "errorRate" | "latencyMs">;

const CIRCUIT_STATES = ["CLOSED", "OPEN", "HALF_OPEN"] as const;
const LATENCY_SAMPLE_LIMIT = 200;

export class ProviderMetrics {
  private readonly registry = new Registry();
  private readonly requestCounter = new Counter({
    name: "hive_provider_requests_total",
    help: "Total provider request attempts recorded by the provider registry.",
    labelNames: ["provider"] as const,
    registers: [this.registry],
  });
  private readonly errorCounter = new Counter({
    name: "hive_provider_errors_total",
    help: "Total provider request attempt errors recorded by the provider registry.",
    labelNames: ["provider"] as const,
    registers: [this.registry],
  });
  private readonly latencyHistogram = new Histogram({
    name: "hive_provider_latency_seconds",
    help: "Provider request attempt latency in seconds.",
    labelNames: ["provider"] as const,
    buckets: [0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10],
    registers: [this.registry],
  });
  private readonly enabledGauge = new Gauge({
    name: "hive_provider_enabled",
    help: "Whether the provider is enabled (1) or disabled (0).",
    labelNames: ["provider"] as const,
    registers: [this.registry],
  });
  private readonly healthyGauge = new Gauge({
    name: "hive_provider_healthy",
    help: "Whether the provider health check is healthy (1) or unhealthy (0).",
    labelNames: ["provider"] as const,
    registers: [this.registry],
  });
  private readonly circuitFailuresGauge = new Gauge({
    name: "hive_provider_circuit_failures",
    help: "Current consecutive circuit-breaker failures for the provider.",
    labelNames: ["provider"] as const,
    registers: [this.registry],
  });
  private readonly circuitStateGauge = new Gauge({
    name: "hive_provider_circuit_state",
    help: "Circuit-breaker state for the provider labelled by state with active state=1.",
    labelNames: ["provider", "state"] as const,
    registers: [this.registry],
  });
  private readonly requestCounts = new Map<ProviderName, number>();
  private readonly errorCounts = new Map<ProviderName, number>();
  private readonly latencySamples = new Map<ProviderName, number[]>();

  constructor(providerNames: readonly ProviderName[]) {
    for (const providerName of providerNames) {
      this.requestCounter.labels(providerName);
      this.errorCounter.labels(providerName);
      this.latencyHistogram.zero({ provider: providerName });
      this.requestCounts.set(providerName, 0);
      this.errorCounts.set(providerName, 0);
      this.latencySamples.set(providerName, []);
    }
  }

  recordAttempt(providerName: ProviderName, latencyMs: number, failed: boolean): void {
    this.requestCounter.inc({ provider: providerName });
    this.requestCounts.set(providerName, (this.requestCounts.get(providerName) ?? 0) + 1);

    if (failed) {
      this.errorCounter.inc({ provider: providerName });
      this.errorCounts.set(providerName, (this.errorCounts.get(providerName) ?? 0) + 1);
    }

    const latencySeconds = Math.max(latencyMs, 0) / 1000;
    this.latencyHistogram.observe({ provider: providerName }, latencySeconds);

    const samples = this.latencySamples.get(providerName) ?? [];
    samples.push(Math.max(latencyMs, 0));
    if (samples.length > LATENCY_SAMPLE_LIMIT) {
      samples.shift();
    }
    this.latencySamples.set(providerName, samples);
  }

  summarize(providers: ProviderStatusSnapshot[]): ProviderMetricsResult {
    this.refreshProviderGauges(providers);

    return {
      scrapedAt: new Date().toISOString(),
      providers: providers.map((provider) => {
        const requests = this.requestCounts.get(provider.name) ?? 0;
        const errors = this.errorCounts.get(provider.name) ?? 0;
        const latencyMs = this.getLatencySummary(provider.name);

        return {
          ...provider,
          requests,
          errors,
          errorRate: requests === 0 ? 0 : Number((errors / requests).toFixed(4)),
          latencyMs,
        };
      }),
    };
  }

  async renderPrometheus(providers: ProviderStatusSnapshot[]): Promise<{ contentType: string; body: string }> {
    this.refreshProviderGauges(providers);
    return {
      contentType: this.registry.contentType,
      body: await this.registry.metrics(),
    };
  }

  private refreshProviderGauges(providers: ProviderStatusSnapshot[]): void {
    for (const provider of providers) {
      this.enabledGauge.set({ provider: provider.name }, provider.enabled ? 1 : 0);
      this.healthyGauge.set({ provider: provider.name }, provider.healthy ? 1 : 0);
      this.circuitFailuresGauge.set({ provider: provider.name }, provider.circuit.failures);

      for (const state of CIRCUIT_STATES) {
        this.circuitStateGauge.set(
          { provider: provider.name, state },
          provider.circuit.state === state ? 1 : 0,
        );
      }
    }
  }

  private getLatencySummary(providerName: ProviderName) {
    const samples = [...(this.latencySamples.get(providerName) ?? [])].sort((a, b) => a - b);
    if (samples.length === 0) {
      return { avg: 0, p95: 0 };
    }

    const total = samples.reduce((sum, value) => sum + value, 0);
    const percentileIndex = Math.max(0, Math.ceil(samples.length * 0.95) - 1);

    return {
      avg: Number((total / samples.length).toFixed(2)),
      p95: Number(samples[percentileIndex].toFixed(2)),
    };
  }
}
