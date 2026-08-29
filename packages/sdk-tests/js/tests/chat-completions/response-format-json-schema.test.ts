import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
// chat-completions.test.ts already covers response_format: json_object on
// this same pinned alias; see its header comment for why hive-free cannot
// stand in here (seeded tools_supported=false, correctly 400s).
const TOOL_CAPABLE_MODEL =
  process.env.HIVE_TOOLS_MODEL ?? "deepseek-v4-flash";

describe("Chat Completions response_format: json_schema", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("returns content conforming to the supplied JSON schema", async () => {
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [
        {
          role: "user",
          content: "Extract: the city is Dhaka and the population is 22 million.",
        },
      ],
      max_tokens: 256,
      response_format: {
        type: "json_schema",
        json_schema: {
          name: "city_info",
          strict: true,
          schema: {
            type: "object",
            properties: {
              city: { type: "string" },
              population_millions: { type: "number" },
            },
            required: ["city", "population_millions"],
            additionalProperties: false,
          },
        },
      },
    });

    expect(response.object).toBe("chat.completion");
    const content = response.choices[0].message.content;
    expect(typeof content).toBe("string");

    const parsed = JSON.parse(content!) as Record<string, unknown>;
    // The schema sets additionalProperties: false, so the object must be
    // exactly these two keys. Checking the two types individually accepted a
    // response carrying extra keys, which is the half of the schema contract
    // that had no coverage at all.
    expect(parsed).toEqual({
      city: expect.any(String),
      population_millions: expect.any(Number),
    });
  });
});
