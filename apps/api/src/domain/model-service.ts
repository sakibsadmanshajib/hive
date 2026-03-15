import type { GatewayModel } from "./types";

const MODELS: GatewayModel[] = [
  {
    id: "guest-free",
    object: "model",
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
    id: "fast-chat",
    object: "model",
    capability: "chat",
    costType: "fixed",
    pricing: {
      creditsPerRequest: 8,
    },
  },
  {
    id: "smart-reasoning",
    object: "model",
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
    capability: "image",
    costType: "fixed",
    pricing: {
      creditsPerRequest: 120,
    },
  },
];

type ModelServiceOptions = {
  enabledFreeModelIds?: Iterable<string>;
};

export class ModelService {
  private readonly enabledFreeModelIds?: Set<string>;

  constructor(options?: ModelServiceOptions) {
    this.enabledFreeModelIds = options?.enabledFreeModelIds
      ? new Set(options.enabledFreeModelIds)
      : undefined;
  }

  list(): GatewayModel[] {
    return MODELS.filter((model) => this.isModelEnabled(model));
  }

  findById(modelId: string): GatewayModel | undefined {
    return MODELS.find((model) => model.id === modelId);
  }

  pickDefault(capability: "chat" | "image"): GatewayModel {
    const selected = MODELS.find((model) => model.capability === capability && model.costType !== "free")
      ?? MODELS.find((model) => model.capability === capability);
    if (!selected) {
      throw new Error(`No model for capability: ${capability}`);
    }
    return selected;
  }

  pickGuestDefault(capability: "chat" | "image"): GatewayModel {
    const selected = MODELS.find((model) => model.capability === capability && model.costType === "free");
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
