import type {
  ProviderChatRequest,
  ProviderChatResponse,
  ProviderClient,
  ProviderHealthStatus,
  ProviderReadinessStatus,
} from "./types";
import { fetchWithRetry } from "./http-client";

type GroqConfig = {
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
};

type GroqChatResponse = {
  choices?: Array<{
    message?: {
      content?: string;
    };
  }>;
};

type GroqModelsResponse = {
  data?: Array<{
    id?: string;
  }>;
};

export class GroqProviderClient implements ProviderClient {
  readonly name = "groq" as const;

  constructor(private readonly config: GroqConfig) {}

  isEnabled(): boolean {
    return Boolean(this.config.baseUrl) && Boolean(this.config.apiKey);
  }

  async chat(request: ProviderChatRequest): Promise<ProviderChatResponse> {
    if (!this.config.apiKey) {
      throw new Error("groq api key missing");
    }

    const response = await fetchWithRetry({
      provider: "groq",
      url: `${this.config.baseUrl}/chat/completions`,
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "POST",
        headers: {
          authorization: `Bearer ${this.config.apiKey}`,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          model: request.model,
          messages: request.messages,
        }),
      },
    });

    if (!response.ok) {
      throw new Error(`groq request failed with status ${response.status}`);
    }

    const payload = (await response.json()) as GroqChatResponse;
    const content = payload.choices?.[0]?.message?.content?.trim();
    if (!content) {
      throw new Error("groq response missing content");
    }

    return {
      content,
      providerModel: request.model,
    };
  }

  async status(): Promise<ProviderHealthStatus> {
    const enabled = this.isEnabled();
    if (!enabled) {
      return { enabled: false, healthy: false, detail: "GROQ_API_KEY or GROQ_BASE_URL missing" };
    }

    try {
      const response = await fetchWithRetry({
        provider: "groq",
        url: `${this.config.baseUrl}/models`,
        timeoutMs: this.config.timeoutMs,
        maxRetries: this.config.maxRetries,
        init: {
          method: "GET",
          headers: {
            authorization: `Bearer ${this.config.apiKey}`,
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

  async checkModelReadiness(model: string): Promise<ProviderReadinessStatus> {
    if (!this.isEnabled()) {
      return { ready: false, detail: "disabled by config" };
    }

    try {
      const response = await fetchWithRetry({
        provider: "groq",
        url: `${this.config.baseUrl}/models`,
        timeoutMs: this.config.timeoutMs,
        maxRetries: this.config.maxRetries,
        init: {
          method: "GET",
          headers: {
            authorization: `Bearer ${this.config.apiKey}`,
          },
        },
      });

      if (!response.ok) {
        return { ready: false, detail: `startup models check failed: ${response.status}` };
      }

      const payload = (await response.json()) as GroqModelsResponse;
      const availableModels = new Set(
        (payload.data ?? []).map((entry) => entry.id).filter((value): value is string => Boolean(value)),
      );

      if (availableModels.has(model)) {
        return { ready: true, detail: "startup model ready" };
      }

      return { ready: false, detail: `startup model missing: ${model}` };
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      return { ready: false, detail: `startup unreachable: ${reason}` };
    }
  }
}
