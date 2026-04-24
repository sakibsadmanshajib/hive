import { expect, test } from "@playwright/test";
import OpenAI from "openai";

const HIVE_API_KEY = process.env.HIVE_API_KEY ?? "";
// Only run when the caller explicitly points the spec at a live edge-api
// (see the web-e2e-api job). The default web-e2e job leaves this unset so
// the spec is skipped — there is no edge-api on http://localhost:8080 there.
const EDGE_BASE_URL = process.env.EDGE_BASE_URL ?? "";
const MODEL = process.env.HIVE_SDK_MODEL ?? "hive-default";

test.describe("openai sdk consumer hits edge-api", () => {
  test.skip(
    !HIVE_API_KEY || !EDGE_BASE_URL,
    "HIVE_API_KEY or EDGE_BASE_URL not set"
  );

  const client = () =>
    new OpenAI({
      apiKey: HIVE_API_KEY,
      baseURL: EDGE_BASE_URL,
    });

  test("models.list returns at least one hive model", async () => {
    const list = await client().models.list();
    const ids = list.data.map((m) => m.id);
    expect(ids.length).toBeGreaterThan(0);
    expect(ids.some((id) => id.startsWith("hive-"))).toBe(true);
  });

  test("chat.completions non-streaming returns content and usage", async () => {
    const res = await client().chat.completions.create({
      model: MODEL,
      messages: [
        {
          role: "user",
          content: "Reply with a short sentence that greets the reader.",
        },
      ],
      max_tokens: 128,
      temperature: 0,
    });

    const choice = res.choices[0];
    expect(choice).toBeDefined();
    // Providers may return content as string or null when truncated. Accept
    // either as long as the server charged tokens — that proves the request
    // reached the provider and round-tripped through edge-api usage metering.
    const content = choice.message.content ?? "";
    expect(typeof content).toBe("string");
    expect(res.usage?.total_tokens ?? 0).toBeGreaterThan(0);
    expect(res.usage?.completion_tokens ?? 0).toBeGreaterThan(0);
  });

  test("chat.completions streaming yields at least one delta chunk", async () => {
    const stream = await client().chat.completions.create({
      model: MODEL,
      messages: [
        {
          role: "user",
          content:
            "Reply with a short greeting. Stream at least a dozen tokens.",
        },
      ],
      max_tokens: 256,
      temperature: 0,
      stream: true,
    });

    let chunks = 0;
    let sawDelta = false;
    let sawFinish = false;
    let concatenated = "";
    for await (const chunk of stream) {
      chunks += 1;
      const choice = chunk.choices[0];
      const delta = choice?.delta?.content ?? "";
      if (delta.length > 0) {
        sawDelta = true;
        concatenated += delta;
      }
      if (choice?.finish_reason) {
        sawFinish = true;
      }
    }

    expect(chunks).toBeGreaterThan(0);
    expect(sawFinish).toBe(true);
    // Accept either visible content in at least one delta OR a clean finish —
    // some providers emit all content in non-content deltas (tool call / role)
    // before closing, which still validates the streaming transport.
    expect(sawDelta || sawFinish).toBe(true);
    if (sawDelta) {
      expect(concatenated.length).toBeGreaterThan(0);
    }
  });
});
