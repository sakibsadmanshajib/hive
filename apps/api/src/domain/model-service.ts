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
    provider: "mock",
  },
  {
    id: "fast-chat",
    object: "model",
    capability: "chat",
    costType: "fixed",
    pricing: {
      creditsPerRequest: 8,
    },
    provider: "ollama",
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
    provider: "groq",
  },
  {
    id: "image-basic",
    object: "model",
    capability: "image",
    costType: "fixed",
    pricing: {
      creditsPerRequest: 120,
    },
    provider: "openai",
  },
];

export class ModelService {
  list(): GatewayModel[] {
    return MODELS;
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
}
