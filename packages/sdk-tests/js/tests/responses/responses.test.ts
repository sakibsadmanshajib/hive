import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-default";

// Known reasoning model identifiers — skip reasoning parameter rejection test for these.
const REASONING_MODEL_PATTERNS = ["o1", "o3", "reasoning"];

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
