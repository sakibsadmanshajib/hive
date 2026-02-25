import type { ProviderChatRequest, ProviderChatResponse, ProviderClient, ProviderHealthStatus } from "./types";
import { fetchWithRetry } from "./http-client";

type OllamaConfig = {
  baseUrl: string;
  timeoutMs: number;
  maxRetries: number;
};

type OllamaChatResponse = {
  message?: {
    content?: string;
  };
};

export class OllamaProviderClient implements ProviderClient {
  readonly name = "ollama" as const;

  constructor(private readonly config: OllamaConfig) {}

  isEnabled(): boolean {
    return Boolean(this.config.baseUrl);
  }

  async chat(request: ProviderChatRequest): Promise<ProviderChatResponse> {
    const response = await fetchWithRetry({
      provider: "ollama",
      url: `${this.config.baseUrl}/api/chat`,
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          model: request.model,
          messages: request.messages,
          stream: false,
        }),
      },
    });

    if (!response.ok) {
      throw new Error(`ollama request failed with status ${response.status}`);
    }

    const payload = (await response.json()) as OllamaChatResponse;
    const content = payload.message?.content?.trim();
    if (!content) {
      throw new Error("ollama response missing content");
    }

    return {
      content,
      providerModel: request.model,
    };
  }

  async status(): Promise<ProviderHealthStatus> {
    const enabled = this.isEnabled();
    if (!enabled) {
      return { enabled: false, healthy: false, detail: "OLLAMA_BASE_URL not configured" };
    }

    try {
      const response = await fetchWithRetry({
        provider: "ollama",
        url: `${this.config.baseUrl}/api/tags`,
        timeoutMs: this.config.timeoutMs,
        maxRetries: this.config.maxRetries,
        init: { method: "GET" },
      });
      if (!response.ok) {
        return { enabled: true, healthy: false, detail: `tags check failed: ${response.status}` };
      }
      return { enabled: true, healthy: true, detail: "reachable" };
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      return { enabled: true, healthy: false, detail: `unreachable: ${reason}` };
    }
  }
}
