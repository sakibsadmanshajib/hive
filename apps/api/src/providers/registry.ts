import type { ProviderChatMessage, ProviderClient, ProviderName } from "./types";

type ProviderRegistryConfig = {
  clients: ProviderClient[];
  defaultProvider: ProviderName;
  modelProviderMap: Record<string, ProviderName>;
  providerModelMap: Record<ProviderName, string>;
  fallbackOrder: Record<ProviderName, ProviderName[]>;
};

export type ProviderExecutionResult = {
  content: string;
  providerUsed: ProviderName;
  providerModel: string;
};

export type ProviderStatusResult = {
  providers: Array<{
    name: ProviderName;
    enabled: boolean;
    healthy: boolean;
    detail: string;
  }>;
};

export class ProviderRegistry {
  private readonly clientsByName: Map<ProviderName, ProviderClient>;

  constructor(private readonly config: ProviderRegistryConfig) {
    this.clientsByName = new Map(config.clients.map((client) => [client.name, client]));
  }

  async chat(modelId: string, messages: ProviderChatMessage[]): Promise<ProviderExecutionResult> {
    const primaryProvider = this.config.modelProviderMap[modelId] ?? this.config.defaultProvider;
    const candidateProviders = this.buildCandidates(primaryProvider);
    const errors: string[] = [];

    for (const providerName of candidateProviders) {
      const client = this.clientsByName.get(providerName);
      if (!client || !client.isEnabled()) {
        continue;
      }

      const providerModel = this.config.providerModelMap[providerName] ?? modelId;
      try {
        const response = await client.chat({ model: providerModel, messages });
        return {
          content: response.content,
          providerUsed: providerName,
          providerModel: response.providerModel ?? providerModel,
        };
      } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        errors.push(`${providerName}: ${reason}`);
      }
    }

    throw new Error(`no provider succeeded${errors.length > 0 ? ` (${errors.join(" | ")})` : ""}`);
  }

  async status(): Promise<ProviderStatusResult> {
    const providers: ProviderStatusResult["providers"] = [];
    for (const providerName of ["ollama", "groq", "mock"] as const) {
      const client = this.clientsByName.get(providerName);
      if (!client) {
        providers.push({
          name: providerName,
          enabled: false,
          healthy: false,
          detail: "not registered",
        });
        continue;
      }
      const health = await client.status();
      providers.push({
        name: providerName,
        enabled: health.enabled,
        healthy: health.healthy,
        detail: health.detail,
      });
    }
    return { providers };
  }

  private buildCandidates(primary: ProviderName): ProviderName[] {
    const ordered = [primary, ...(this.config.fallbackOrder[primary] ?? [])];
    return [...new Set(ordered)];
  }
}
