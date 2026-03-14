import { describe, expect, it } from "vitest";

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
});
