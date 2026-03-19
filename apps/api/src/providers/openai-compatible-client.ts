import type {
  ProviderChatRequest,
  ProviderChatResponse,
  ProviderEmbeddingsRequest,
  ProviderEmbeddingsResponse,
  ProviderHealthStatus,
  ProviderImageRequest,
  ProviderImageResponse,
  ProviderName,
  ProviderReadinessStatus,
} from "./types";
import { fetchWithRetry } from "./http-client";

type OpenAICompatibleModelsResponse = {
  data?: Array<{
    id?: string;
  }>;
};

type OpenAICompatibleChatResponse = {
  id?: string;
  object?: string;
  created?: number;
  model?: string;
  choices?: Array<{
    index?: number;
    finish_reason?: string;
    message?: {
      role?: string;
      content?: string;
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

type OpenAICompatibleImageResponse = {
  created?: number;
  data?: Array<{
    url?: string;
    b64_json?: string;
    revised_prompt?: string;
  }>;
};

export type OpenAICompatibleClientConfig = {
  name: ProviderName;
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
  missingConfigDetail: string;
  extraHeaders?: Record<string, string>;
};

function joinUrl(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/+$/, "")}/${path.replace(/^\/+/, "")}`;
}

export class OpenAICompatibleProviderClient {
  readonly name: ProviderName;

  constructor(protected readonly config: OpenAICompatibleClientConfig) {
    this.name = config.name;
  }

  isEnabled(): boolean {
    return Boolean(this.config.baseUrl) && Boolean(this.config.apiKey);
  }

  async chat(request: ProviderChatRequest): Promise<ProviderChatResponse> {
    if (!this.config.apiKey) {
      throw new Error(`${this.name} api key missing`);
    }

    const response = await fetchWithRetry({
      provider: this.name,
      url: joinUrl(this.config.baseUrl, "/chat/completions"),
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "POST",
        headers: {
          authorization: `Bearer ${this.config.apiKey}`,
          "content-type": "application/json",
          ...(this.config.extraHeaders ?? {}),
        },
        body: JSON.stringify({
          model: request.model,
          messages: request.messages,
          ...request.params,
          stream: false,
        }),
      },
    });

    if (!response.ok) {
      let errorMessage = `${this.name} request failed with status ${response.status}`;
      try {
        const errorBody = await response.json() as { error?: { message?: string } };
        if (errorBody?.error?.message) {
          errorMessage = errorBody.error.message;
        }
      } catch { /* ignore parse failures */ }
      const err = new Error(errorMessage);
      (err as any).statusCode = response.status;
      throw err;
    }

    const payload = (await response.json()) as OpenAICompatibleChatResponse;
    const content = payload.choices?.[0]?.message?.content?.trim() ?? "";

    return {
      content,
      providerModel: payload.model ?? request.model,
      usage: payload.usage
        ? {
          promptTokens: payload.usage.prompt_tokens ?? 0,
          completionTokens: payload.usage.completion_tokens ?? 0,
          totalTokens: payload.usage.total_tokens
            ?? (payload.usage.prompt_tokens ?? 0) + (payload.usage.completion_tokens ?? 0),
        }
        : undefined,
      rawResponse: payload,
    };
  }

  async chatStream(request: ProviderChatRequest): Promise<Response> {
    if (!this.config.apiKey) {
      throw new Error(`${this.name} api key missing`);
    }

    const response = await fetchWithRetry({
      provider: this.name,
      url: joinUrl(this.config.baseUrl, "/chat/completions"),
      timeoutMs: 120_000,
      maxRetries: 0,
      init: {
        method: "POST",
        headers: {
          authorization: `Bearer ${this.config.apiKey}`,
          "content-type": "application/json",
          ...(this.config.extraHeaders ?? {}),
        },
        body: JSON.stringify({
          model: request.model,
          messages: request.messages,
          ...request.params,
          stream: true,
        }),
      },
    });

    if (!response.ok) {
      let errorMessage = `${this.name} request failed with status ${response.status}`;
      try {
        const errorBody = await response.json() as { error?: { message?: string } };
        if (errorBody?.error?.message) {
          errorMessage = errorBody.error.message;
        }
      } catch { /* ignore parse failures */ }
      const err = new Error(errorMessage);
      (err as any).statusCode = response.status;
      throw err;
    }

    return response;
  }

  async generateImage(request: ProviderImageRequest): Promise<ProviderImageResponse> {
    if (!this.config.apiKey) {
      throw new Error(`${this.name} api key missing`);
    }

    const body: Record<string, unknown> = {
      model: request.model,
      prompt: request.prompt,
      n: request.n,
      size: request.size,
      response_format: request.responseFormat,
    };
    if (request.quality) body.quality = request.quality;
    if (request.style) body.style = request.style;
    if (request.user) body.user = request.user;

    const response = await fetchWithRetry({
      provider: this.name,
      url: joinUrl(this.config.baseUrl, "/images/generations"),
      timeoutMs: this.config.timeoutMs,
      maxRetries: 0,
      init: {
        method: "POST",
        headers: {
          authorization: `Bearer ${this.config.apiKey}`,
          "content-type": "application/json",
          ...(this.config.extraHeaders ?? {}),
        },
        body: JSON.stringify(body),
      },
    });

    if (!response.ok) {
      let errorMessage = `${this.name} request failed with status ${response.status}`;
      try {
        const errorBody = await response.json() as { error?: { message?: string } };
        if (errorBody?.error?.message) {
          errorMessage = errorBody.error.message;
        }
      } catch { /* ignore parse failures */ }
      throw new Error(errorMessage);
    }

    const payload = (await response.json()) as OpenAICompatibleImageResponse;
    const data = (payload.data ?? []).map((entry) => ({
      ...(entry.url ? { url: entry.url } : {}),
      ...(entry.b64_json ? { b64Json: entry.b64_json } : {}),
      ...(entry.revised_prompt ? { revisedPrompt: entry.revised_prompt } : {}),
    }));
    if (data.length === 0) {
      throw new Error(`${this.name} response missing image data`);
    }

    return {
      created: payload.created ?? Math.floor(Date.now() / 1000),
      data,
      providerModel: request.model,
    };
  }

  async embeddings(request: ProviderEmbeddingsRequest): Promise<ProviderEmbeddingsResponse> {
    if (!this.config.apiKey) {
      throw new Error(`${this.name} api key missing`);
    }

    const body: Record<string, unknown> = {
      model: request.model,
      input: request.input,
    };
    if (request.encodingFormat !== undefined) body.encoding_format = request.encodingFormat;
    if (request.dimensions !== undefined) body.dimensions = request.dimensions;
    if (request.user !== undefined) body.user = request.user;

    const response = await fetchWithRetry({
      provider: this.name,
      url: joinUrl(this.config.baseUrl, "/embeddings"),
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "POST",
        headers: {
          authorization: `Bearer ${this.config.apiKey}`,
          "content-type": "application/json",
          ...(this.config.extraHeaders ?? {}),
        },
        body: JSON.stringify(body),
      },
    });

    if (!response.ok) {
      let errorMessage = `${this.name} request failed with status ${response.status}`;
      try {
        const errorBody = await response.json() as { error?: { message?: string } };
        if (errorBody?.error?.message) {
          errorMessage = errorBody.error.message;
        }
      } catch { /* ignore parse failures */ }
      const err = new Error(errorMessage);
      (err as any).statusCode = response.status;
      throw err;
    }

    const payload = await response.json() as {
      data?: Array<{ embedding?: number[]; index?: number }>;
      model?: string;
      usage?: { prompt_tokens?: number; total_tokens?: number };
    };

    return {
      data: (payload.data ?? []).map((item, i) => ({
        embedding: item.embedding ?? [],
        index: item.index ?? i,
      })),
      model: payload.model ?? request.model,
      providerModel: payload.model ?? request.model,
      usage: payload.usage
        ? {
          promptTokens: payload.usage.prompt_tokens ?? 0,
          totalTokens: payload.usage.total_tokens ?? 0,
        }
        : undefined,
      rawResponse: payload,
    };
  }

  async status(): Promise<ProviderHealthStatus> {
    const enabled = this.isEnabled();
    if (!enabled) {
      return { enabled: false, healthy: false, detail: this.config.missingConfigDetail };
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

      const payload = (await response.json()) as OpenAICompatibleModelsResponse;
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

  protected fetchModels(): Promise<Response> {
    return fetchWithRetry({
      provider: this.name,
      url: joinUrl(this.config.baseUrl, "/models"),
      timeoutMs: this.config.timeoutMs,
      maxRetries: this.config.maxRetries,
      init: {
        method: "GET",
        headers: {
          authorization: `Bearer ${this.config.apiKey}`,
          ...(this.config.extraHeaders ?? {}),
        },
      },
    });
  }

  protected joinUrl(path: string): string {
    return joinUrl(this.config.baseUrl, path);
  }
}
