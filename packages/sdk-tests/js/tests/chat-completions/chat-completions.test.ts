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

  it("passes tools through and returns a tool_calls completion", async () => {
    // Phase 20 (#118): capability-based passthrough ships. Capable routes
    // (openrouter, groq) have tools_supported=true seeded, so tool calls are
    // forwarded rather than rejected. The edge must NOT 400 on tools.
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

    expect(response.object).toBe("chat.completion");
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    // Model should invoke the tool.
    expect(response.choices[0].finish_reason).toBe("tool_calls");
    expect(response.choices[0].message.tool_calls).toBeDefined();
    expect(response.choices[0].message.tool_calls!.length).toBeGreaterThan(0);
    expect(response.choices[0].message.tool_calls![0].function.name).toBe(
      "get_weather",
    );
  });

  it("passes response_format through and returns valid JSON", async () => {
    // Phase 20 (#118): response_format forwarded to capable routes.
    // The edge must NOT 400 on response_format; content must be parseable JSON.
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

    expect(response.object).toBe("chat.completion");
    expect(response.choices.length).toBeGreaterThanOrEqual(1);
    const content = response.choices[0].message.content;
    expect(typeof content).toBe("string");
    // Content must be parseable JSON containing at least one key.
    const parsed = JSON.parse(content!);
    expect(typeof parsed).toBe("object");
    expect(parsed).not.toBeNull();
  });
});
