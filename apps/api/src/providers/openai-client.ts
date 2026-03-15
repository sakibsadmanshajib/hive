import type {
  ProviderClient,
  ProviderHealthStatus,
  ProviderImageRequest,
  ProviderImageResponse,
  ProviderReadinessStatus,
} from "./types";
import type { ProviderChatRequest, ProviderChatResponse } from "./types";
import { fetchWithRetry } from "./http-client";
import { OpenAICompatibleProviderClient } from "./openai-compatible-client";

type OpenAIConfig = {
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
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
  private readonly chatClient: OpenAICompatibleProviderClient;

  constructor(private readonly config: OpenAIConfig) {
    this.chatClient = new OpenAICompatibleProviderClient({
      name: "openai",
      baseUrl: this.config.baseUrl,
      apiKey: this.config.apiKey,
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      missingConfigDetail: "OPENAI_API_KEY or OPENAI_BASE_URL missing",
    });
  }

  isEnabled(): boolean {
    return this.chatClient.isEnabled();
  }

  async chat(request: ProviderChatRequest): Promise<ProviderChatResponse> {
    return this.chatClient.chat(request);
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
    return this.chatClient.status();
  }

  async checkModelReadiness(model: string): Promise<ProviderReadinessStatus> {
    return this.chatClient.checkModelReadiness(model);
  }
}
