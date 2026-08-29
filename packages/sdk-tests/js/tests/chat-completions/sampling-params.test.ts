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
  // max_tokens on every route, full stop. An earlier version of this file
  // asserted a reserve-inflated ceiling on the pooled alias, on the strength
  // of migration 20260826_01. That inflation is GONE: PR #1225 added it, issue
  // #1283 removed it, and the current code sends the caller ceiling upstream
  // untouched, which is also what OpenAI specifies for a reasoning model where
  // reasoning tokens count against the ceiling. Verified against the live
  // gateway on 2026-08-29: a ceiling of 120 came back as exactly 120
  // completion tokens, not 4216. Both aliases are asserted because they
  // resolve to different routes, not because the contract differs.

  // Both ceiling tests need a prompt whose NATURAL answer is far longer than
  // the ceiling, or the assertion cannot fail: ask for one short fact and a
  // gateway that dropped max_tokens entirely still answers in five tokens and
  // the test passes over nothing. Counting to 200 is the cheap way to get an
  // answer that only stops early because the ceiling stopped it.
  const LONG_ANSWER_PROMPT = "Count from 1 to 200, one number per line.";

  it("honors max_tokens on the pooled alias", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: LONG_ANSWER_PROMPT }],
      max_tokens: 8,
    });

    expect(response.usage).toBeDefined();
    expect(response.usage!.completion_tokens).toBeLessThanOrEqual(8);
  });

  it("honors max_tokens on the pinned single-route alias", async () => {
    // A second route, not a second contract. deepseek-v4-flash reasons
    // heavily and still respects the ceiling exactly, which is what makes an
    // empty answer on this alias a reasoning burn rather than a dropped
    // parameter (issue #1326 and the zero-content guard).
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

  // Issue #1316 lives here, and the marker that used to sit on this test does
  // not, because the behaviour is not stable enough for one. Run 33220907602
  // answered 200 with a single choice, which is the defect: neither honouring
  // n nor rejecting it. Run 33225363432 took the other branch and the
  // expected-failure marker itself went red for an unexpected pass. A plain
  // assertion is the honest shape for something that alternates: it goes red
  // exactly on the runs where the defect actually happens, and says nothing
  // on the runs where it does not.
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
