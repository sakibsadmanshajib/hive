import { OpenAICompatibleProviderClient } from "./openai-compatible-client";

type OpenRouterConfig = {
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
};

export class OpenRouterProviderClient extends OpenAICompatibleProviderClient {
  readonly name = "openrouter" as const;

  constructor(config: OpenRouterConfig) {
    super({
      name: "openrouter",
      baseUrl: config.baseUrl,
      apiKey: config.apiKey,
      timeoutMs: config.timeoutMs,
      maxRetries: config.maxRetries,
      missingConfigDetail: "OPENROUTER_API_KEY or OPENROUTER_BASE_URL missing",
    });
  }
}
