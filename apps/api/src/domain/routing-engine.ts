export interface ModelProfile {
  modelId: string;
  capabilities: Set<string>;
  creditsPer1kInput: number;
  creditsPer1kOutput: number;
  quality: number;
  latencyMs: number;
}

export function blendedCost(model: ModelProfile): number {
  return (model.creditsPer1kInput + model.creditsPer1kOutput) / 2;
}

export class RoutingEngine {
  private readonly models: ModelProfile[];

  constructor(models: Iterable<ModelProfile>) {
    this.models = [...models];
  }

  pickAuto(taskType: string, minQuality: number, maxLatencyMs: number): ModelProfile {
    const eligible = this.models.filter(
      (model) =>
        model.capabilities.has(taskType) && model.quality >= minQuality && model.latencyMs <= maxLatencyMs,
    );
    if (eligible.length === 0) {
      throw new Error("no eligible model");
    }

    return [...eligible].sort((a, b) => {
      const costDiff = blendedCost(a) - blendedCost(b);
      if (costDiff !== 0) {
        return costDiff;
      }
      return b.quality - a.quality;
    })[0];
  }

  listModels(): ModelProfile[] {
    return [...this.models];
  }
}
