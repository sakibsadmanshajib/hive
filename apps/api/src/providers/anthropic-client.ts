import type {
  ProviderChatRequest,
  ProviderChatResponse,
  ProviderClient,
  ProviderHealthStatus,
  ProviderReadinessStatus,
} from "./types";
import { fetchWithRetry } from "./http-client";

type AnthropicConfig = {
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
};

type AnthropicMessagesResponse = {
  model?: string;
  content?: Array<{
    type?: string;
    text?: string;
  }>;
  usage?: {
    input_tokens?: number;
    output_tokens?: number;
  };
};

type AnthropicModelsResponse = {
  data?: Array<{
    id?: string;
  }>;
};

function joinUrl(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/+$/, "")}/${path.replace(/^\/+/, "")}`;
}

export class AnthropicProviderClient implements ProviderClient {
  readonly name = "anthropic" as const;

  constructor(private readonly config: AnthropicConfig) {}

  isEnabled(): boolean {
    return Boolean(this.config.baseUrl) && Boolean(this.config.apiKey);
  }

  async chat(request: ProviderChatRequest): Promise<ProviderChatResponse> {
    const authHeaders = this.getAuthHeaders();

    const system = request.messages
      .filter((message) => message.role === "system")
      .map((message) => message.content)
      .join("\n\n")
      .trim();
    const messages = request.messages
      .filter((message) => message.role !== "system")
      .map((message) => ({
        role: message.role,
        content: message.content,
      }));

    const response = await fetchWithRetry({
      provider: "anthropic",
      url: joinUrl(this.config.baseUrl, "/messages"),
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "POST",
        headers: {
          ...authHeaders,
          "content-type": "application/json",
        },
        body: JSON.stringify({
          model: request.model,
          messages,
          max_tokens: 1024,
          ...(system ? { system } : {}),
        }),
      },
    });

    if (!response.ok) {
      throw new Error(`anthropic request failed with status ${response.status}`);
    }

    const payload = (await response.json()) as AnthropicMessagesResponse;
    const content = (payload.content ?? [])
      .filter((entry) => entry.type === "text" && entry.text)
      .map((entry) => entry.text?.trim())
      .filter((value): value is string => Boolean(value))
      .join("\n\n");
    if (!content) {
      throw new Error("anthropic response missing content");
    }

    return {
      content,
      providerModel: payload.model ?? request.model,
      usage: payload.usage
        ? {
          promptTokens: payload.usage.input_tokens ?? 0,
          completionTokens: payload.usage.output_tokens ?? 0,
          totalTokens: (payload.usage.input_tokens ?? 0) + (payload.usage.output_tokens ?? 0),
        }
        : undefined,
    };
  }

  async status(): Promise<ProviderHealthStatus> {
    const enabled = this.isEnabled();
    if (!enabled) {
      return { enabled: false, healthy: false, detail: "ANTHROPIC_API_KEY or ANTHROPIC_BASE_URL missing" };
    }

    try {
      const response = await this.fetchModels();
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
      const response = await this.fetchModels();

      if (!response.ok) {
        return { ready: false, detail: `startup models check failed: ${response.status}` };
      }

      const payload = (await response.json()) as AnthropicModelsResponse;
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

  private fetchModels(): Promise<Response> {
    return fetchWithRetry({
      provider: "anthropic",
      url: joinUrl(this.config.baseUrl, "/models"),
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "GET",
        headers: this.getAuthHeaders(),
      },
    });
  }

  private getAuthHeaders(): Record<string, string> {
    if (!this.config.apiKey) {
      throw new Error("anthropic api key missing");
    }

    return {
      "x-api-key": this.config.apiKey,
      "anthropic-version": "2023-06-01",
    };
  }
}
