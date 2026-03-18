export type ProviderName = "mock" | "ollama" | "groq" | "openai" | "openrouter" | "gemini" | "anthropic";

export type ProviderCostClass = "zero" | "fixed" | "variable";

export type ProviderChatMessage = {
  role: "system" | "user" | "assistant";
  content: string;
};

export type ProviderChatRequest = {
  model: string;
  messages: ProviderChatMessage[];
  params?: Record<string, unknown>;
};

export type ProviderChatResponse = {
  content: string;
  providerModel?: string;
  usage?: {
    promptTokens: number;
    completionTokens: number;
    totalTokens: number;
  };
  rawResponse?: {
    id?: string;
    object?: string;
    created?: number;
    model?: string;
    choices?: Array<{
      index?: number;
      finish_reason?: string;
      message?: {
        role?: string;
        content?: string | null;
        refusal?: string | null;
        tool_calls?: unknown[];
      };
      logprobs?: unknown | null;
    }>;
    usage?: {
      prompt_tokens?: number;
      completion_tokens?: number;
      total_tokens?: number;
    };
  };
};

export type ProviderImageRequest = {
  model: string;
  prompt: string;
  n: number;
  size?: string;
  responseFormat: "url" | "b64_json";
  user?: string;
};

export type ProviderImageData = {
  url?: string;
  b64Json?: string;
};

export type ProviderImageResponse = {
  created: number;
  data: ProviderImageData[];
  providerModel?: string;
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
  chatStream?(request: ProviderChatRequest): Promise<Response>;
  generateImage?(request: ProviderImageRequest): Promise<ProviderImageResponse>;
  status(): Promise<ProviderHealthStatus>;
  checkModelReadiness(model: string): Promise<ProviderReadinessStatus>;
}
