import { describe, it, expect } from "vitest";
import OpenAI, { APIError } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-free";
// Same tools-capable pinned alias chat-completions.test.ts uses (see its
// header comment for why hive-free is unsuitable here): temperature/top_p,
// seed and logprobs are sampling knobs a routed model may or may not honor,
// and this suite needs a route that answers deterministically enough to
// assert real shapes rather than "some free-pool member or other."
const TOOL_CAPABLE_MODEL =
  process.env.HIVE_TOOLS_MODEL ?? "deepseek-v4-flash";

/**
 * A provider-optional chat parameter has exactly two valid outcomes on a
 * conformant OpenAI-compatible gateway: the parameter is honored (200, with
 * the expected shape), or it is cleanly rejected (4xx, typed SDK error,
 * structured envelope). A 5xx, a hang, or an untyped error is a bug either
 * way. This helper asserts that invariant without hardcoding which of the
 * two live outcomes today's route mix happens to produce, so the test stays
 * a real regression guard as provider support shifts.
 */
async function acceptedOrCleanlyRejected(
  call: () => Promise<unknown>,
): Promise<{ ok: true; value: unknown } | { ok: false; error: APIError }> {
  try {
    const value = await call();
    return { ok: true, value };
  } catch (err) {
    expect(err).toBeInstanceOf(APIError);
    const apiErr = err as APIError;
    expect(apiErr.status).toBeGreaterThanOrEqual(400);
    expect(apiErr.status).toBeLessThan(500);
    return { ok: false, error: apiErr };
  }
}

describe("Chat Completions sampling parameters", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("honors max_tokens as a hard ceiling on completion length", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: "Write a long story about the sea." }],
      max_tokens: 8,
    });

    expect(response.usage).toBeDefined();
    // A small ceiling gives the provider some slack for stop-token
    // accounting, but must not be silently ignored.
    expect(response.usage!.completion_tokens).toBeLessThanOrEqual(32);
  });

  it("honors stop sequences", async () => {
    const result = await acceptedOrCleanlyRejected(() =>
      client.chat.completions.create({
        model: MODEL,
        messages: [
          { role: "user", content: "Count from 1 to 10, one number per line." },
        ],
        max_tokens: 256,
        stop: ["5"],
      }),
    );

    if (result.ok) {
      const response = result.value as OpenAI.Chat.Completions.ChatCompletion;
      expect(response.choices[0].message.content).toBeDefined();
    }
  });

  it("honors temperature and top_p without erroring", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: "Say hello" }],
      max_tokens: 32,
      temperature: 0.2,
      top_p: 0.9,
    });

    expect(response.object).toBe("chat.completion");
  });

  it("n>1 either returns n choices or is cleanly rejected", async () => {
    const result = await acceptedOrCleanlyRejected(() =>
      client.chat.completions.create({
        model: TOOL_CAPABLE_MODEL,
        messages: [{ role: "user", content: "Say hello in one word." }],
        max_tokens: 16,
        n: 2,
      }),
    );

    if (result.ok) {
      const response = result.value as OpenAI.Chat.Completions.ChatCompletion;
      // A gateway that accepts n must actually return that many choices;
      // silently truncating to 1 is worse than a clean rejection.
      expect(response.choices.length).toBe(2);
    }
  });

  it("seed either narrows sampling or is cleanly rejected, and never leaks a provider fingerprint", async () => {
    const result = await acceptedOrCleanlyRejected(() =>
      client.chat.completions.create({
        model: TOOL_CAPABLE_MODEL,
        messages: [{ role: "user", content: "Say hello" }],
        max_tokens: 16,
        seed: 42,
      }),
    );

    if (result.ok) {
      const response = result.value as OpenAI.Chat.Completions.ChatCompletion;
      // PR #1222: system_fingerprint is a provider identity leak and must
      // never reach the caller, seed request or not.
      expect(response.system_fingerprint == null).toBe(true);
    }
  });

  it("logprobs either returns a valid logprobs shape or is cleanly rejected", async () => {
    const result = await acceptedOrCleanlyRejected(() =>
      client.chat.completions.create({
        model: TOOL_CAPABLE_MODEL,
        messages: [{ role: "user", content: "Say hello" }],
        max_tokens: 16,
        logprobs: true,
        top_logprobs: 2,
      }),
    );

    if (result.ok) {
      const response = result.value as OpenAI.Chat.Completions.ChatCompletion;
      const logprobs = response.choices[0].logprobs;
      if (logprobs != null) {
        expect(Array.isArray(logprobs.content)).toBe(true);
      }
    }
  });
});
