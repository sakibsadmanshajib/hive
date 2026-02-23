import { describe, expect, it } from "vitest";

import { ModelProfile, RoutingEngine } from "../../src/domain/routing-engine";

describe("RoutingEngine", () => {
  it("auto-selects lowest-cost acceptable model", () => {
    const models: ModelProfile[] = [
      {
        modelId: "fast-chat",
        capabilities: new Set(["chat"]),
        creditsPer1kInput: 10,
        creditsPer1kOutput: 12,
        quality: 0.6,
        latencyMs: 450,
      },
      {
        modelId: "smart-chat",
        capabilities: new Set(["chat", "reasoning"]),
        creditsPer1kInput: 20,
        creditsPer1kOutput: 24,
        quality: 0.9,
        latencyMs: 700,
      },
      {
        modelId: "cheap-low",
        capabilities: new Set(["chat"]),
        creditsPer1kInput: 5,
        creditsPer1kOutput: 5,
        quality: 0.2,
        latencyMs: 300,
      },
    ];

    const engine = new RoutingEngine(models);
    const routed = engine.pickAuto("chat", 0.5, 1000);

    expect(routed.modelId).toBe("fast-chat");
  });

  it("throws when no model is eligible", () => {
    const engine = new RoutingEngine([
      {
        modelId: "image",
        capabilities: new Set(["image"]),
        creditsPer1kInput: 12,
        creditsPer1kOutput: 15,
        quality: 0.7,
        latencyMs: 600,
      },
    ]);

    expect(() => engine.pickAuto("chat", 0.5, 500)).toThrowError("no eligible model");
  });
});
