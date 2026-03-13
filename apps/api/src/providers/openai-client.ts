import type {
  ProviderChatRequest,
  ProviderChatResponse,
  ProviderClient,
  ProviderHealthStatus,
  ProviderImageRequest,
  ProviderImageResponse,
  ProviderReadinessStatus,
} from "./types";
import { fetchWithRetry } from "./http-client";

type OpenAIConfig = {
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
};

type OpenAIModelsResponse = {
  data?: Array<{
    id?: string;
  }>;
};

type OpenAIImageResponse = {
  created?: number;
  data?: Array<{
    url?: string;
    b64_json?: string;
  }>;
};

export class OpenAIProviderClient implements ProviderClient {
  readonly name = "openai" as const;

  constructor(private readonly config: OpenAIConfig) {}

  isEnabled(): boolean {
    return Boolean(this.config.baseUrl) && Boolean(this.config.apiKey);
  }

  async chat(_request: ProviderChatRequest): Promise<ProviderChatResponse> {
    throw new Error("openai chat not implemented");
  }

  async generateImage(request: ProviderImageRequest): Promise<ProviderImageResponse> {
    if (!this.config.apiKey) {
      throw new Error("openai api key missing");
    }

    const response = await fetchWithRetry({
      provider: "openai",
      url: `${this.config.baseUrl}/images/generations`,
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
          prompt: request.prompt,
          n: request.n,
          size: request.size,
          response_format: request.responseFormat,
          user: request.user,
        }),
      },
    });

    if (!response.ok) {
      throw new Error(`openai request failed with status ${response.status}`);
    }

    const payload = (await response.json()) as OpenAIImageResponse;
    const data =
      payload.data?.map((entry) => ({
        url: entry.url,
        b64Json: entry.b64_json,
      })) ?? [];
    if (data.length === 0) {
      throw new Error("openai response missing image data");
    }

    return {
      created: payload.created ?? Math.floor(Date.now() / 1000),
      data,
      providerModel: request.model,
    };
  }

  async status(): Promise<ProviderHealthStatus> {
    const enabled = this.isEnabled();
    if (!enabled) {
      return { enabled: false, healthy: false, detail: "OPENAI_API_KEY or OPENAI_BASE_URL missing" };
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

      const payload = (await response.json()) as OpenAIModelsResponse;
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
      provider: "openai",
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
  }
}
