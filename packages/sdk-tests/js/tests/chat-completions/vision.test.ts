import { describe, it, expect } from "vitest";
import OpenAI, { APIError } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const TOOL_CAPABLE_MODEL =
  process.env.HIVE_TOOLS_MODEL ?? "hive-small";

// 1x1 red pixel PNG, inlined so this test needs no network fetch of its own
// and no fixture file. Small enough that a vision-incapable route's context
// limit is never the reason for a rejection.
const RED_PIXEL_PNG_DATA_URI =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=";

describe("Chat Completions vision (image_url input)", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("accepts image_url content and either answers or is cleanly rejected", async () => {
    try {
      const response = await client.chat.completions.create({
        model: TOOL_CAPABLE_MODEL,
        messages: [
          {
            role: "user",
            content: [
              { type: "text", text: "What color is this image? One word." },
              { type: "image_url", image_url: { url: RED_PIXEL_PNG_DATA_URI } },
            ],
          },
        ],
        max_tokens: 32,
      });

      expect(response.object).toBe("chat.completion");
      expect(typeof response.choices[0].message.content).toBe("string");
    } catch (err) {
      // A route with no vision capability must reject cleanly (4xx, typed
      // SDK error), never 500 or hang. Vision support is not declared per
      // alias in the support matrix today (issue filed), so both outcomes
      // are valid; a 5xx or a network-level failure is not.
      expect(err).toBeInstanceOf(APIError);
      const apiErr = err as APIError;
      expect(apiErr.status).toBeGreaterThanOrEqual(400);
      expect(apiErr.status).toBeLessThan(500);
    }
  });
});
