import type {
  ProviderChatRequest,
  ProviderChatResponse,
  ProviderClient,
  ProviderHealthStatus,
  ProviderReadinessStatus,
} from "./types";

export class MockProviderClient implements ProviderClient {
  readonly name = "mock" as const;

  isEnabled(): boolean {
    return true;
  }

  async chat(request: ProviderChatRequest): Promise<ProviderChatResponse> {
    const prompt = request.messages.filter((message) => message.role === "user").map((message) => message.content).join(" ");
    return {
      content: `MVP response: ${prompt || "Your request was processed."}`,
      providerModel: request.model,
    };
  }

  async status(): Promise<ProviderHealthStatus> {
    return {
      enabled: true,
      healthy: true,
      detail: "always available fallback",
    };
  }

  async checkModelReadiness(_model: string): Promise<ProviderReadinessStatus> {
    return {
      ready: true,
      detail: "startup model ready",
    };
  }
}
