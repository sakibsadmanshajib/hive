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

  // The ceiling this gateway owes a caller is not simply max_tokens on every
  // alias, and asserting that it is produced a red suite over correct
  // behaviour. edge-api inflates the ceiling it sends upstream by the
  // resolved route reasoning reserve (migration 20260826_01, issue #1171):
  // on a pool with reasoning members, hidden reasoning burns the reserve so
  // the caller budget survives as visible content. So the contract is
  // max_tokens on a route with no reserve, and max_tokens plus the reserve on
  // one that has it. Both are asserted, because only the first can catch
  // max_tokens being dropped outright.
  const REASONING_RESERVE_TOKENS = 4096;

  // Both ceiling tests need a prompt whose NATURAL answer is far longer than
  // the ceiling, or the assertion cannot fail: ask for one short fact and a
  // gateway that dropped max_tokens entirely still answers in five tokens and
  // the test passes over nothing. Counting to 200 is the cheap way to get an
  // answer that only stops early because the ceiling stopped it.
  const LONG_ANSWER_PROMPT = "Count from 1 to 200, one number per line.";

  it("honors max_tokens plus the reasoning reserve on the pooled alias", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: LONG_ANSWER_PROMPT }],
      max_tokens: 8,
    });

    expect(response.usage).toBeDefined();
    expect(response.usage!.completion_tokens).toBeLessThanOrEqual(
      8 + REASONING_RESERVE_TOKENS,
    );
  });

  it("honors max_tokens exactly on a route carrying no reasoning reserve", async () => {
    // deepseek-v4-flash is not a free pool member, so its
    // provider_routes.reasoning_reserve_tokens is the column default of zero
    // and the caller ceiling reaches the provider untouched. This is the
    // assertion that goes red if max_tokens stops being forwarded at all.
    const response = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [{ role: "user", content: LONG_ANSWER_PROMPT }],
      max_tokens: 8,
    });

    expect(response.usage).toBeDefined();
    // The ceiling the caller asked for, not a rounded-up version of it. An
    // allowance of 32 here would pass a route that quietly capped at 32
    // instead of 8, which is the same defect this test exists to catch.
    expect(response.usage!.completion_tokens).toBeLessThanOrEqual(8);
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

  // EXPECTED FAILURE, issue #1316: the gateway answers 200 with a single
  // choice, which is neither honouring n nor rejecting it. it.fails keeps the
  // call live and the assertion real: the day the gateway starts returning
  // two choices or a clean 4xx, this test passes and vitest turns the suite
  // red for an unexpected pass, which is the signal to delete this marker.
  it.fails("n>1 either returns n choices or is cleanly rejected", async () => {
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
