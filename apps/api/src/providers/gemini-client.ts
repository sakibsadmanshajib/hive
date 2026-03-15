import { OpenAICompatibleProviderClient } from "./openai-compatible-client";

type GeminiConfig = {
  baseUrl: string;
  apiKey?: string;
  timeoutMs: number;
  maxRetries: number;
};

export class GeminiProviderClient extends OpenAICompatibleProviderClient {
  readonly name = "gemini" as const;

  constructor(config: GeminiConfig) {
    super({
      name: "gemini",
      baseUrl: config.baseUrl,
      apiKey: config.apiKey,
      timeoutMs: config.timeoutMs,
      maxRetries: config.maxRetries,
      missingConfigDetail: "GEMINI_API_KEY or GEMINI_BASE_URL missing",
    });
  }
}
