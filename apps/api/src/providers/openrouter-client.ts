import type { ProviderChatRequest, ProviderChatResponse, ProviderClient, ProviderHealthStatus } from "./types";
import { fetchWithRetry } from "./http-client";

type OpenRouterConfig = {
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
};

type OpenRouterChatResponse = {
  choices?: Array<{
    message?: {
      content?: string;
    };
  }>;
  model?: string;
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
    total_tokens?: number;
  };
};

export class OpenRouterProviderClient implements ProviderClient {
  readonly name = "openrouter" as const;

  constructor(private readonly config: OpenRouterConfig) {}

  isEnabled(): boolean {
    return Boolean(this.config.baseUrl) && Boolean(this.config.apiKey);
  }

  async chat(request: ProviderChatRequest): Promise<ProviderChatResponse> {
    if (!this.config.apiKey) {
      throw new Error("openrouter api key missing");
    }

    const response = await fetchWithRetry({
      provider: "openrouter",
      url: `${this.config.baseUrl}/chat/completions`,
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.config.apiKey}`,
          "Content-Type": "application/json",
          "HTTP-Referer": "https://hive-ai.com", // Required by OpenRouter
          "X-Title": "Hive AI Gateway",
        },
        body: JSON.stringify({
          model: request.model,
          messages: request.messages,
          stream: false,
        }),
      },
    });

    if (!response.ok) {
      throw new Error(`openrouter request failed with status ${response.status}`);
    }

    const payload = (await response.json()) as OpenRouterChatResponse;
    const content = payload.choices?.[0]?.message?.content?.trim();
    if (!content) {
      throw new Error("openrouter response missing content");
    }

    return {
      content,
      providerModel: payload.model || request.model,
      usage: payload.usage ? {
        promptTokens: payload.usage.prompt_tokens || 0,
        completionTokens: payload.usage.completion_tokens || 0,
        totalTokens: payload.usage.total_tokens || 0,
      } : undefined,
    };
  }

  async status(): Promise<ProviderHealthStatus> {
    const enabled = this.isEnabled();
    if (!enabled) {
      return { enabled: false, healthy: false, detail: "OPENROUTER_API_KEY or OPENROUTER_BASE_URL missing" };
    }

    try {
      const response = await fetchWithRetry({
        provider: "openrouter",
        url: `${this.config.baseUrl}/models`,
        timeoutMs: this.config.timeoutMs,
        maxRetries: this.config.maxRetries,
        init: {
          method: "GET",
          headers: {
            Authorization: `Bearer ${this.config.apiKey}`,
          },
        },
      });

      if (!response.ok) {
        return { enabled: true, healthy: false, detail: `models check failed: ${response.status}` };
      }
      return { enabled: true, healthy: true, detail: "reachable" };
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      return { enabled: true, healthy: false, detail: `unreachable: ${reason}` };
    }
  }
}