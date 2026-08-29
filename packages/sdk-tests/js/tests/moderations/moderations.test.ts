import { describe, it, expect } from "vitest";
import OpenAI, { APIError } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";

// The support matrix declares POST /v1/moderations
// explicitly_unsupported_at_launch. This suite asserts that declaration is
// honest: the SDK must see a structured, typed 404, not a silent pass-
// through or a 500. If this starts failing because the route now answers
// 200, that is a real signal (either the matrix is stale and needs
// updating, or the route regressed into an undeclared, ungated new surface)
// and not a suite bug.
describe("Moderations (declared explicitly_unsupported_at_launch)", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("moderations.create throws a structured unsupported-endpoint error", async () => {
    try {
      await client.moderations.create({ input: "This is a harmless sentence." });
      expect.fail("expected moderations.create to be rejected as unsupported");
    } catch (err) {
      expect(err).toBeInstanceOf(APIError);
      const apiErr = err as APIError;
      expect(apiErr.status).toBe(404);
      const body = apiErr.error as Record<string, unknown> | undefined;
      expect(body?.type).toBe("unsupported_endpoint");
      // "endpoint_unsupported" is the code the matrix middleware emits for
      // explicitly_unsupported_at_launch routes, distinct from the
      // "endpoint_not_available" code planned_for_launch routes get (see
      // errors/unsupported-endpoint.test.ts for both).
      expect(body?.code).toBe("endpoint_unsupported");
      const message = body?.message as string | undefined;
      expect(message ?? "").not.toMatch(
        /provider|upstream|openai|groq|openrouter|deepseek|gemini/i,
      );
    }
  });
});
