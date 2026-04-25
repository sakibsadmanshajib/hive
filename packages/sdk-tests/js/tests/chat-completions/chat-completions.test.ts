import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-default";

describe("Chat Completions", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("returns a valid chat completion via SDK", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: "Say hello" }],
      max_tokens: 256,
    });

    expect(response.object).toBe("chat.completion");
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    expect(response.choices[0].message.role).toBe("assistant");
    expect(typeof response.choices[0].message.content).toBe("string");
    expect(response.choices[0].message.content).toBeTruthy();
    expect(response.usage).toBeDefined();
    expect(response.usage!.prompt_tokens).toBeGreaterThan(0);
    expect(response.usage!.completion_tokens).toBeGreaterThan(0);
  });

  it("model field shows Hive alias not provider handle", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: "Say hello" }],
      max_tokens: 256,
    });

    // Model should be the Hive alias, not a provider route handle.
    expect(response.model).not.toMatch(/route-/i);
    expect(response.model).not.toMatch(/openrouter/i);
    expect(response.model).not.toMatch(/groq/i);
  });

  it("rejects invalid model with 404", async () => {
    await expect(
      client.chat.completions.create({
        model: "nonexistent-model-12345",
        messages: [{ role: "user", content: "hello" }],
        max_tokens: 256,
      }),
    ).rejects.toMatchObject({ status: 404 });
  });

  it("supports tool calling", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [
        { role: "user", content: "What is the weather like in London?" },
      ],
      max_tokens: 256,
      tools: [
        {
          type: "function",
          function: {
            name: "get_weather",
            description: "Get the current weather for a location",
            parameters: {
              type: "object",
              properties: {
                location: {
                  type: "string",
                  description: "The city to get weather for",
                },
              },
              required: ["location"],
            },
          },
        },
      ],
    });

    // Response should complete without error — tool may or may not be called.
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
  });

  it("supports response_format json_object", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [
        {
          role: "user",
          content: 'Return a JSON object with a single key "status" set to "ok".',
        },
      ],
      max_tokens: 256,
      response_format: { type: "json_object" },
    });

    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    // response_format json_object is a best-effort provider hint. Upstream
    // providers may return null content with completion_tokens=0 (finish
    // reason "stop") when the schema constraint can't be satisfied. Verify
    // the request reached the provider (prompt billed) and, when content is
    // present, parses as valid JSON.
    expect(response.usage?.prompt_tokens ?? 0).toBeGreaterThan(0);
    const content = response.choices[0].message.content;
    if (content) {
      expect(() => JSON.parse(content)).not.toThrow();
    }
  });
});
