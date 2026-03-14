import { describe, expect, it, vi } from "vitest";

import { AiService } from "../../src/domain/ai-service";
import { CreditService } from "../../src/domain/credit-service";
import { ModelService } from "../../src/domain/model-service";
import { UsageService } from "../../src/domain/usage-service";

describe("AiService", () => {
  it("fails before consuming credits when usage channel context is missing", () => {
    const credits = new CreditService();
    const ai = new AiService(new ModelService(), credits, new UsageService());
    const before = credits.getBalance("user-1").availableCredits;

    expect(() => ai.responses("user-1", "hello", undefined as never)).toThrowError();
    expect(credits.getBalance("user-1").availableCredits).toBe(before);
  });

  it("honors an explicit ImageRequest.model instead of always using the default image model", () => {
    const usageAdd = vi.fn();
    const ai = new AiService(
      {
        pickDefault: (capability: "chat" | "image") => {
          if (capability === "image") {
            return {
              id: "image-default",
              capability: "image",
              pricing: { creditsPerRequest: 10 },
            };
          }
          throw new Error("unexpected capability");
        },
        findById: (modelId: string) => (
          modelId === "image-custom"
            ? {
                id: "image-custom",
                capability: "image",
                pricing: { creditsPerRequest: 33 },
              }
            : undefined
        ),
        creditsForRequest: (model: { pricing?: { creditsPerRequest?: number } }) => model.pricing?.creditsPerRequest ?? 0,
      } as never,
      { consume: () => true } as never,
      { add: usageAdd } as never,
    );

    const result = ai.imageGeneration("user-1", {
      prompt: "draw a harbor",
      model: "image-custom",
    }, {
      channel: "api",
    });

    expect(result).toMatchObject({
      statusCode: 200,
      headers: { "x-actual-credits": "33" },
    });
    expect(usageAdd).toHaveBeenCalledWith(expect.objectContaining({
      model: "image-custom",
      credits: 33,
    }));
  });
});
