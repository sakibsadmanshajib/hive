import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-free";
const TOOL_CAPABLE_MODEL =
  process.env.HIVE_TOOLS_MODEL ?? "hive-small";

// D-056 / apps/edge-api/internal/inference/types.go: prompt_tokens_details
// carries cached_tokens (json:"cached_tokens", not omitempty), and it is the
// single field every OpenAI-compatible agent tool reads to show cache
// savings. This suite pins that shape down at the SDK level, which no
// existing test in this repo does.
describe("Usage accounting", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("prompt_tokens_details.cached_tokens is present with a numeric value", async () => {
    const response = await client.chat.completions.create({
      model: MODEL,
      messages: [{ role: "user", content: "Say hello" }],
      max_tokens: 32,
    });

    expect(response.usage).toBeDefined();
    expect(response.usage!.prompt_tokens).toBeGreaterThan(0);
    expect(response.usage!.completion_tokens).toBeGreaterThan(0);
    expect(response.usage!.total_tokens).toBe(
      response.usage!.prompt_tokens + response.usage!.completion_tokens,
    );

    // The OpenAI wire contract makes prompt_tokens_details optional, but a
    // caller depending on cache-savings display (the entire point of this
    // field, per the owner brief) needs it present and numeric, not merely
    // "sometimes there." First-call cache reads are legitimately 0, so this
    // only pins the shape, not a nonzero value.
    const details = response.usage!.prompt_tokens_details;
    expect(details).toBeDefined();
    expect(typeof details?.cached_tokens).toBe("number");
  });

  it("a second identical request either reuses cache (cached_tokens > 0) or reports a clean zero, never a negative or missing value", async () => {
    const messages: OpenAI.Chat.Completions.ChatCompletionMessageParam[] = [
      {
        role: "user",
        content:
          "Repeat this exact sentence back to me verbatim: the quick brown fox jumps over the lazy dog, twenty five times over, once per line, numbered.",
      },
    ];

    await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages,
      max_tokens: 32,
    });
    const second = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages,
      max_tokens: 32,
    });

    const cached = second.usage?.prompt_tokens_details?.cached_tokens;
    expect(typeof cached).toBe("number");
    expect(cached as number).toBeGreaterThanOrEqual(0);
  });

  // The day arrived. Issue #1317 is closed, the terminal usage frame is
  // relayed (PR #1334), and the first live run afterwards reported this as an
  // unexpected pass, so the marker comes off exactly as the old comment
  // promised. The assertions below are unchanged: what used to be a
  // documented gap is now a plain regression guard.
  it("stream_options.include_usage emits a terminal usage chunk with prompt_tokens_details", async () => {
    // Pinned to the single-route capable alias, not hive-free: hive-free is
    // a load-balanced pool across four heterogeneous keys (D-048), and the
    // skipped local regression test in streaming-chat.test.ts documents that
    // a blended pool's trailing-usage-frame behavior is not uniform member
    // to member. A single pinned route is a fair, non-flaky regression
    // target for this assertion; hive-free's own behavior here is exactly
    // the kind of per-member gap this suite's live run is meant to surface,
    // tracked separately rather than asserted strictly in every CI run.
    const stream = await client.chat.completions.create({
      model: TOOL_CAPABLE_MODEL,
      messages: [{ role: "user", content: "Say hi" }],
      stream: true,
      stream_options: { include_usage: true },
      max_tokens: 64,
    });

    const chunks: OpenAI.Chat.Completions.ChatCompletionChunk[] = [];
    for await (const chunk of stream) {
      chunks.push(chunk);
    }

    // A terminal usage chunk has empty choices and a populated usage object.
    const terminalChunk = chunks.find(
      (chunk) => chunk.choices.length === 0 && chunk.usage != null,
    );

    expect(terminalChunk).toBeDefined();
    expect(terminalChunk!.usage!.prompt_tokens).toBeGreaterThan(0);
    expect(terminalChunk!.usage!.completion_tokens).toBeGreaterThan(0);
    expect(
      typeof terminalChunk!.usage!.prompt_tokens_details?.cached_tokens,
    ).toBe("number");
  });
});
