import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
// chat-completions.test.ts already covers response_format: json_object on
// this same pinned alias; see its header comment for why hive-free cannot
// stand in here (seeded tools_supported=false, correctly 400s), and for the
// live evidence that hive-small's upstream returns message.content as a
// string under both response_format modes.
const TOOL_CAPABLE_MODEL =
  process.env.HIVE_TOOLS_MODEL ?? "hive-free";

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
      // 512, not 256. This alias reasons, and reasoning tokens count against
      // the ceiling: measured live on 2026-08-29, a second-turn answer on this
      // route spent 22 to 182 tokens thinking before writing anything. At 256
      // a structured-output task occasionally spends the whole ceiling on
      // reasoning and returns nothing, which is a defect in what the ceiling
      // is set to here, not in the schema handling this test exists to check.
      // The gateway-side half of that story is the zero-content retry guard.
      max_tokens: 512,
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
    // Named explicitly, because JSON.parse on an empty string throws
    // "Unexpected end of JSON input", which says nothing about what went
    // wrong. An empty answer here is the reasoning-burn shape (issue #1326),
    // not a schema violation, and the two deserve different diagnoses.
    expect(content!.length).toBeGreaterThan(0);

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
