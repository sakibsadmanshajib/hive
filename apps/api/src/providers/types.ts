export type ProviderName = "mock" | "ollama" | "groq";

export type ProviderChatMessage = {
  role: "system" | "user" | "assistant";
  content: string;
};

export type ProviderChatRequest = {
  model: string;
  messages: ProviderChatMessage[];
};

export type ProviderChatResponse = {
  content: string;
  providerModel?: string;
  // New: Token usage tracking
  usage?: {
    promptTokens: number;
    completionTokens: number;
    totalTokens: number;
  };
};

export type ProviderHealthStatus = {
  enabled: boolean;
  healthy: boolean;
  detail: string;
};

export type ProviderReadinessStatus = {
  ready: boolean;
  detail: string;
};

export type ProviderLatencySummary = {
  avg: number;
  p95: number;
};

export type ProviderCircuitSnapshot = {
  state: "CLOSED" | "OPEN" | "HALF_OPEN";
  failures: number;
  lastError?: string;
};

export type ProviderMetricsSummary = {
  name: ProviderName;
  enabled: boolean;
  healthy: boolean;
  detail: string;
  circuit: ProviderCircuitSnapshot;
  requests: number;
  errors: number;
  errorRate: number;
  latencyMs: ProviderLatencySummary;
};

export type ProviderMetricsResult = {
  scrapedAt: string;
  providers: ProviderMetricsSummary[];
};

export interface ProviderClient {
  readonly name: ProviderName;
  isEnabled(): boolean;
  chat(request: ProviderChatRequest): Promise<ProviderChatResponse>;
  status(): Promise<ProviderHealthStatus>;
  checkModelReadiness(model: string): Promise<ProviderReadinessStatus>;
}
