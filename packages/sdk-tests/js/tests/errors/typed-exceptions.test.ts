import { describe, it, expect } from "vitest";
import OpenAI, { APIError } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-free";
// Deliberately garbage, not a real credential shape. Named apart from the
// call site so a repo secret-scanning hook never mistakes an inline literal
// for a live key.
const GARBAGE_CREDENTIAL = ["not", "a", "real", "hive", "credential"].join("-");

// Agent frameworks (LangChain, the OpenAI Agents SDK, etc.) branch on the
// SDK's typed error subclasses, not on status codes read out of a generic
// catch. A gateway that returns the right status code but an envelope the
// SDK cannot parse into the matching subclass looks fine in curl and breaks
// every caller that does `catch (AuthenticationError) { ... }`.
// unsupported-endpoint.test.ts already pins NotFoundError for the two
// declared-unsupported cases; this file covers the rest of the family this
// suite can trigger without flooding the shared live gateway (RateLimitError
// specifically is not exercised here: forcing one deterministically means
// hammering the API past its limit, which conflicts with keeping load light
// against a pool shared with live chat traffic. Untested, and said so here
// rather than silently assumed).
describe("SDK typed exception classes", () => {
  it("an invalid API key raises AuthenticationError", async () => {
    const client = new OpenAI({ baseURL: BASE_URL, apiKey: GARBAGE_CREDENTIAL });

    await expect(
      client.models.list(),
    ).rejects.toBeInstanceOf(OpenAI.AuthenticationError);
  });

  it("a request with no Authorization header is a structured 401", async () => {
    // Deliberately raw HTTP rather than the SDK. openai v7 refuses to build
    // a client with an empty apiKey and throws locally before any bytes
    // leave the process, so the SDK form of this test asserted the SDK
    // constructor and never reached the gateway at all. What is worth
    // asserting is the gateway contract, so this speaks HTTP directly.
    const res = await fetch(BASE_URL + "/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: MODEL,
        messages: [{ role: "user", content: "hi" }],
      }),
    });

    expect(res.status).toBe(401);
    const body = (await res.json()) as {
      error?: { type?: string; message?: string };
    };
    expect(typeof body.error?.type).toBe("string");
    expect(typeof body.error?.message).toBe("string");
    expect(JSON.stringify(body)).not.toMatch(/openrouter|groq|gemini/i);
  });

  it("an invalid model raises NotFoundError", async () => {
    const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

    await expect(
      client.chat.completions.create({
        model: "definitely-not-a-hive-alias",
        messages: [{ role: "user", content: "hi" }],
      }),
    ).rejects.toBeInstanceOf(OpenAI.NotFoundError);
  });

  it("a structurally invalid message role raises BadRequestError", async () => {
    const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

    await expect(
      client.chat.completions.create({
        model: MODEL,
        // @ts-expect-error -- deliberately invalid, asserting server-side validation
        messages: [{ role: "not-a-real-role", content: "hi" }],
      }),
    ).rejects.toBeInstanceOf(OpenAI.BadRequestError);
  });

  it("an empty messages array raises BadRequestError", async () => {
    const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

    await expect(
      client.chat.completions.create({
        model: MODEL,
        messages: [],
      }),
    ).rejects.toBeInstanceOf(OpenAI.BadRequestError);
  });

  it("a large request body either succeeds (bounded output cost) or is cleanly rejected, never hangs or 5xxs", async () => {
    const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });
    // ~30KB of content. It was ~300KB, and that single line metered about
    // 70,000 prompt tokens against the shared free pool in one CI run, on its
    // own blowing the whole job token ceiling: nothing rejects a body this
    // size on the chat path, so every byte was forwarded upstream and billed
    // as prompt. max_tokens: 1 only bounds the OUTPUT, which was never where
    // the cost was. The boundary behaviour is still what is under test
    // (whichever layer owns it: app, Caddy, or Cloudflare), and it is just as
    // observable at a tenth of the spend against an allowance shared with the
    // live demo.
    const bigContent = "word ".repeat(6_000);

    try {
      const response = await client.chat.completions.create({
        model: MODEL,
        messages: [{ role: "user", content: bigContent }],
        max_tokens: 1,
      });
      expect(response.object).toBe("chat.completion");
    } catch (err) {
      expect(err).toBeInstanceOf(APIError);
      const apiErr = err as APIError;
      expect(apiErr.status).toBeGreaterThanOrEqual(400);
      // Below 500, not below 600. This test is named for never returning a
      // 5xx, and an upper bound of 600 accepted exactly the failure it claims
      // to rule out.
      expect(apiErr.status).toBeLessThan(500);
    }
  }, 30000);
});
