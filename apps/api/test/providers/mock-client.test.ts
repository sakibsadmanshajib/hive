import { describe, expect, it } from "vitest";
import { MockProviderClient } from "../../src/providers/mock-client";

describe("mock provider client", () => {
  it("supports image generation as a fallback provider", async () => {
    const client = new MockProviderClient();

    await expect(
      client.generateImage?.({
        model: "mock-image",
        prompt: "city skyline",
        n: 1,
        size: "1024x1024",
        responseFormat: "url",
      }),
    ).resolves.toEqual({
      created: expect.any(Number),
      data: [{ url: "https://example.invalid/generated/city%20skyline.png" }],
      providerModel: "mock-image",
    });
  });
});
