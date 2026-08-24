import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-free";

// Known reasoning model identifiers — skip the reasoning-parameter rejection
// test for these, because the edge routes that parameter on
// provider_capabilities.supports_reasoning and these aliases have it true, so
// there is no rejection to assert.
//
// The free pool alias (`hive-free`, migration
// 20260824_02_free_pool_router.sql) is seeded supports_reasoning true: every
// member of its load-balanced pool accepts reasoning parameters without
// failing, even where the knob modulates little.
//
// `hive-default` and `hive-auto` are listed because HIVE_TEST_MODEL is a knob
// and an override run pointed at either would otherwise assert a rejection
// that cannot happen: hive-default now serves deepseek-v4-flash and hive-auto
// serves the OpenRouter Auto Router (both seeded supports_reasoning true),
// which retires the old "Groq gpt-oss aliases" explanation those five entries
// carried.
//
// `deepseek` covers deepseek-v4-flash and deepseek-v4-pro. Not an accommodation
// for CI: supabase/migrations/20260822_02_catalog_alias_restructure.sql seeds
// both with supports_reasoning true and a "reasoning" capability badge, from
// the provider's own supported_parameters list.
const REASONING_MODEL_PATTERNS = [
  "o1",
  "o3",
  "reasoning",
  "hive-free",
  "hive-default",
  "hive-auto",
  "hive-small",
  "hive-medium",
  "hive-fast",
  "deepseek",
];

function isReasoningModel(model: string): boolean {
  return REASONING_MODEL_PATTERNS.some((pattern) =>
    model.toLowerCase().includes(pattern),
  );
}

describe("Responses API", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("returns a valid response via SDK", async () => {
    const response = await client.responses.create({
      model: MODEL,
      input: "Say hello",
      max_output_tokens: 256,
    });

    expect(response.object).toBe("response");
    expect(response.status).toBe("completed");
    expect(response.output.length).toBeGreaterThanOrEqual(1);

    // Find the message output item (type narrowing required by SDK union type).
    const messageItem = response.output.find((item) => item.type === "message");
    expect(messageItem).toBeDefined();
    const msg = messageItem as OpenAI.Responses.ResponseOutputMessage;
    expect(msg.content.length).toBeGreaterThanOrEqual(1);
    const textContent = msg.content.find((c) => c.type === "output_text");
    expect(textContent).toBeDefined();
    expect((textContent as OpenAI.Responses.ResponseOutputText).text).toBeTruthy();

    expect(response.usage).toBeDefined();
    expect(response.usage!.input_tokens).toBeGreaterThan(0);
    expect(response.usage!.output_tokens).toBeGreaterThan(0);
  });

  it("model field shows Hive alias not provider handle", async () => {
    const response = await client.responses.create({
      model: MODEL,
      input: "Say hello",
      max_output_tokens: 256,
    });

    // Model should be the Hive alias, not a provider route handle.
    expect(response.model).not.toMatch(/route-/i);
    expect(response.model).not.toMatch(/openrouter/i);
    expect(response.model).not.toMatch(/groq/i);
  });

  it("rejects reasoning params on non-reasoning model", async () => {
    if (isReasoningModel(MODEL)) {
      // Skip: this model supports reasoning parameters.
      return;
    }

    await expect(
      client.responses.create({
        model: MODEL,
        input: "Say hello",
        max_output_tokens: 256,
        reasoning: { effort: "medium" },
      }),
    ).rejects.toSatisfy(
      (err: unknown) =>
        err instanceof Error &&
        (err.message.toLowerCase().includes("unsupported") ||
          err.message.toLowerCase().includes("reasoning")),
    );
  });
});
