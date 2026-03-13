import type { GatewayModel } from "./types";

const MODELS: GatewayModel[] = [
  { id: "fast-chat", object: "model", capability: "chat", creditsPerRequest: 8, provider: "ollama" },
  { id: "smart-reasoning", object: "model", capability: "chat", creditsPerRequest: 16, provider: "groq" },
  { id: "image-basic", object: "model", capability: "image", creditsPerRequest: 120, provider: "openai" },
];

export class ModelService {
  list(): GatewayModel[] {
    return MODELS;
  }

  findById(modelId: string): GatewayModel | undefined {
    return MODELS.find((model) => model.id === modelId);
  }

  pickDefault(capability: "chat" | "image"): GatewayModel {
    const selected = MODELS.find((model) => model.capability === capability);
    if (!selected) {
      throw new Error(`No model for capability: ${capability}`);
    }
    return selected;
  }
}
