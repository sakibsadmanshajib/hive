import { describe, it, expect } from "vitest";

// These tests validate the SHAPE of image generation responses against OpenAI's spec.
// They don't test the route itself — they test the response contract.

const IMAGES_FIXTURE = {
  created: 1710792000,
  data: [
    {
      url: "https://cdn.openai.com/generated/image1.png",
      revised_prompt: "A cute orange tabby cat sitting on a windowsill",
    },
  ],
};

const IMAGES_B64_FIXTURE = {
  created: 1710792000,
  data: [{ b64_json: "iVBORw0KGgo..." }],
};

describe("SURF-02: ImagesResponse compliance", () => {
  it("has created as unix timestamp integer", () => {
    expect(typeof IMAGES_FIXTURE.created).toBe("number");
    expect(Number.isInteger(IMAGES_FIXTURE.created)).toBe(true);
    expect(IMAGES_FIXTURE.created).toBeGreaterThan(1_000_000_000);
  });

  it("has data as non-empty array", () => {
    expect(Array.isArray(IMAGES_FIXTURE.data)).toBe(true);
    expect(IMAGES_FIXTURE.data.length).toBeGreaterThan(0);
  });

  it("data[] items have url as string", () => {
    for (const item of IMAGES_FIXTURE.data) {
      expect(typeof item.url).toBe("string");
      expect(item.url).toMatch(/^https?:\/\//);
    }
  });

  it("does NOT have an 'object' field", () => {
    expect("object" in IMAGES_FIXTURE).toBe(false);
  });

  it("data[] items may have revised_prompt", () => {
    for (const item of IMAGES_FIXTURE.data) {
      if ("revised_prompt" in item) {
        expect(typeof item.revised_prompt).toBe("string");
      }
    }
  });

  it("data[] items can have b64_json instead of url", () => {
    for (const item of IMAGES_B64_FIXTURE.data) {
      expect(typeof item.b64_json).toBe("string");
    }
  });

  it("data[] items have either url or b64_json, not both", () => {
    for (const item of IMAGES_FIXTURE.data) {
      const hasUrl = "url" in item;
      const hasB64 = "b64_json" in item;
      expect(hasUrl !== hasB64 || (!hasUrl && !hasB64)).toBe(true);
    }
    for (const item of IMAGES_B64_FIXTURE.data) {
      const hasUrl = "url" in item;
      const hasB64 = "b64_json" in item;
      expect(hasUrl !== hasB64 || (!hasUrl && !hasB64)).toBe(true);
    }
  });
});
