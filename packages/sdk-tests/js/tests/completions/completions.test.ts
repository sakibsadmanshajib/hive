import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-default";

describe("Completions (legacy)", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("returns a valid text completion via SDK", async () => {
    const response = await client.completions.create({
      model: MODEL,
      prompt: "Hello, world",
    });

    expect(response.object).toBe("text_completion");
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    expect(typeof response.choices[0].text).toBe("string");
    expect(response.usage).toBeDefined();
  });

  it("model field shows Hive alias not provider handle", async () => {
    const response = await client.completions.create({
      model: MODEL,
      prompt: "Hello",
    });

    // Model should be the Hive alias, not a provider route handle.
    expect(response.model).not.toMatch(/route-/i);
    expect(response.model).not.toMatch(/openrouter/i);
    expect(response.model).not.toMatch(/groq/i);
  });
});
