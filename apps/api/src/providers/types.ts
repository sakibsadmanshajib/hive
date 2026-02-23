export type ProviderName = "mock" | "ollama" | "groq";

export type ProviderChatMessage = {
  role: "system" | "user" | "assistant";
  content: string;
};

export type ProviderChatRequest = {
  model: string;
  messages: ProviderChatMessage[];
};

export type ProviderChatResponse = {
  content: string;
  providerModel?: string;
};

export type ProviderHealthStatus = {
  enabled: boolean;
  healthy: boolean;
  detail: string;
};

export interface ProviderClient {
  readonly name: ProviderName;
  isEnabled(): boolean;
  chat(request: ProviderChatRequest): Promise<ProviderChatResponse>;
  status(): Promise<ProviderHealthStatus>;
}
