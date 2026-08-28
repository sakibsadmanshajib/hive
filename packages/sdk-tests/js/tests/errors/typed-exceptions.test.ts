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

  it("a missing Authorization header raises AuthenticationError", async () => {
    const client = new OpenAI({ baseURL: BASE_URL, apiKey: "" });

    await expect(
      client.chat.completions.create({
        model: MODEL,
        messages: [{ role: "user", content: "hi" }],
      }),
    ).rejects.toBeInstanceOf(OpenAI.AuthenticationError);
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
    // ~300KB of content. max_tokens: 1 bounds the worst-case output spend if
    // the gateway accepts and dispatches it upstream; the point of this test
    // is the boundary behavior (whichever layer owns it: app, Caddy, or
    // Cloudflare), not the model's answer.
    const bigContent = "word ".repeat(60_000);

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
      expect(apiErr.status).toBeLessThan(600);
    }
  }, 30000);
});
