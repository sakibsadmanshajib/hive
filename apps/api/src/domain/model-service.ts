import type { GatewayModel } from "./types";
import { resolveModelAlias } from "../config/model-aliases";

export function deriveOwnedBy(modelId: string): string {
  if (modelId.startsWith("openrouter/")) return "openrouter";
  if (modelId.startsWith("openai/") || modelId.startsWith("gpt-") || modelId.startsWith("dall-e") || modelId.startsWith("o1") || modelId.startsWith("o3") || modelId.startsWith("o4")) return "openai";
  if (modelId.startsWith("anthropic/") || modelId.startsWith("claude-")) return "anthropic";
  if (modelId.startsWith("google/") || modelId.startsWith("gemini-")) return "google";
  if (modelId.startsWith("x-ai/") || modelId.startsWith("grok-")) return "x-ai";
  if (modelId.startsWith("meta-llama/") || modelId.startsWith("llama-")) return "meta";
  const slash = modelId.indexOf("/");
  if (slash > 0) return modelId.substring(0, slash);
  return "hive";
}

export function serializeModel(model: GatewayModel): {
  id: string; object: "model"; created: number; owned_by: string;
} {
  return {
    id: model.id,
    object: "model",
    created: model.created,
    owned_by: deriveOwnedBy(model.id),
  };
}

const MODELS: GatewayModel[] = [
  {
    id: "guest-free",
    object: "model",
    created: 1700000000,
    capability: "chat",
    costType: "free",
    pricing: {
      creditsPerRequest: 0,
      inputTokensPer1m: 0,
      outputTokensPer1m: 0,
      cacheReadTokensPer1m: 0,
      cacheWriteTokensPer1m: 0,
    },
  },
  {
    id: "smart-reasoning",
    object: "model",
    created: 1700000000,
    capability: "chat",
    costType: "variable",
    pricing: {
      creditsPerRequest: 16,
      inputTokensPer1m: 4,
      outputTokensPer1m: 12,
    },
  },
  {
    id: "image-basic",
    object: "model",
    created: 1700000000,
    capability: "image",
    costType: "fixed",
    pricing: {
      creditsPerRequest: 120,
    },
  },
  {
    id: "gpt-4o",
    object: "model",
    created: 1715367600,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 8, inputTokensPer1m: 2.5, outputTokensPer1m: 10 },
  },
  {
    id: "gpt-4o-mini",
    object: "model",
    created: 1721347200,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 2, inputTokensPer1m: 0.15, outputTokensPer1m: 0.6 },
  },
  {
    id: "gpt-4.1",
    object: "model",
    created: 1744675200,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 8, inputTokensPer1m: 2, outputTokensPer1m: 8 },
  },
  {
    id: "gpt-4.1-mini",
    object: "model",
    created: 1744675200,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 2, inputTokensPer1m: 0.4, outputTokensPer1m: 1.6 },
  },
  {
    id: "gpt-4.1-nano",
    object: "model",
    created: 1744675200,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 1, inputTokensPer1m: 0.1, outputTokensPer1m: 0.4 },
  },
  {
    id: "o4-mini",
    object: "model",
    created: 1744675200,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 4, inputTokensPer1m: 1.1, outputTokensPer1m: 4.4 },
  },
  {
    id: "claude-sonnet-4-20250514",
    object: "model",
    created: 1747267200,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 10, inputTokensPer1m: 3, outputTokensPer1m: 15 },
  },
  {
    id: "claude-haiku-3.5",
    object: "model",
    created: 1729728000,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 2, inputTokensPer1m: 0.8, outputTokensPer1m: 4 },
  },
  {
    id: "gemini-2.5-flash",
    object: "model",
    created: 1744675200,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 2, inputTokensPer1m: 0.15, outputTokensPer1m: 0.6 },
  },
  {
    id: "dall-e-3",
    object: "model",
    created: 1696118400,
    capability: "image",
    costType: "fixed",
    pricing: { creditsPerRequest: 120 },
  },
  {
    id: "text-embedding-3-small",
    object: "model",
    created: 1705948997,
    capability: "embedding",
    costType: "variable",
    pricing: { inputTokensPer1m: 2 },
  },
  {
    id: "openrouter/auto",
    object: "model",
    created: 1700000000,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 8, inputTokensPer1m: 2.5, outputTokensPer1m: 10 },
  },
];

type ModelServiceOptions = {
  enabledFreeModelIds?: Iterable<string>;
  extraModels?: GatewayModel[];
};

export class ModelService {
  private readonly enabledFreeModelIds?: Set<string>;
  private readonly models: GatewayModel[];

  constructor(options?: ModelServiceOptions) {
    this.enabledFreeModelIds = options?.enabledFreeModelIds
      ? new Set(options.enabledFreeModelIds)
      : undefined;
    this.models = mergeModels(MODELS, options?.extraModels ?? []);
  }

  list(): GatewayModel[] {
    return this.enabledModels();
  }

  findById(modelId: string): GatewayModel | undefined {
    const resolved = resolveModelAlias(modelId);
    return this.enabledModels().find((model) => model.id === resolved);
  }

  pickDefault(capability: "chat" | "image" | "embedding"): GatewayModel {
    const candidates = this.enabledModels().filter((model) => model.capability === capability);
    const selected = candidates.find((model) => model.costType !== "free")
      ?? candidates[0];
    if (!selected) {
      throw new Error(`No model for capability: ${capability}`);
    }
    return selected;
  }

  pickGuestDefault(capability: "chat" | "image" | "embedding"): GatewayModel {
    const selected = this.enabledModels().find((model) => model.capability === capability && model.costType === "free");
    if (!selected) {
      throw new Error(`No guest model for capability: ${capability}`);
    }
    return selected;
  }

  isGuestAccessible(modelId: string): boolean {
    const model = this.findById(modelId);
    return model?.capability === "chat" && model.costType === "free";
  }

  creditsForRequest(model: GatewayModel): number {
    return model.pricing.creditsPerRequest ?? 0;
  }

  private enabledModels(): GatewayModel[] {
    return this.models.filter((model) => this.isModelEnabled(model));
  }

  private isModelEnabled(model: GatewayModel): boolean {
    if (model.costType !== "free") {
      return true;
    }
    if (!this.enabledFreeModelIds) {
      return true;
    }
    return this.enabledFreeModelIds.has(model.id);
  }
}

function mergeModels(baseModels: GatewayModel[], extraModels: GatewayModel[]): GatewayModel[] {
  if (extraModels.length === 0) {
    return baseModels;
  }

  const merged = new Map(baseModels.map((model) => [model.id, model]));
  for (const model of extraModels) {
    merged.set(model.id, model);
  }
  return Array.from(merged.values());
}
