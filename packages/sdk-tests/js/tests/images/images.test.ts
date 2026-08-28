import { describe, it, expect } from "vitest";
import OpenAI, { APIError } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
// hive-auto is the only alias in the catalog carrying
// provider_capabilities.supports_image_generation = true (route-groq-auto ->
// groq/openai/gpt-oss-120b, a text chat model). The migration that seeded it
// (20260822_02_catalog_alias_restructure.sql, section 6b comment) is explicit
// that this is a carried-forward legacy flag, not a claim that this route can
// actually generate an image: "do not read these flags as a claim that
// gpt-oss-120b does images." No other alias declares the capability at all,
// so this is the only model value that can even reach SelectRoute for a
// NeedImageGeneration request.
const IMAGE_MODEL = process.env.HIVE_IMAGE_MODEL ?? "hive-auto";

describe("Images", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  // EXPECTED FAILURE, issue #1319: the call answers 200 with an empty data
  // array, which is neither an image nor an error a caller can act on.
  //
  // The success assertions moved OUT of the try block, and that is a fix in
  // its own right, not cosmetics: a failed expect() is itself a thrown
  // exception, so inside the try the catch below caught this suite own
  // assertion and re-reported it as "not an APIError". The real defect was
  // invisible behind a confusing message about instanceof.
  it.fails("images.generate either returns a real image or fails with a structured, provider-blind error (never a 5xx)", async () => {
    let response: Awaited<ReturnType<typeof client.images.generate>>;
    try {
      response = await client.images.generate({
        model: IMAGE_MODEL,
        prompt: "a single red circle on a white background",
        n: 1,
      });
    } catch (err) {
      // The support matrix declares POST /v1/images/generations
      // supported_now (phase 6). If the catalog cannot actually serve an
      // image today, the contract this gateway owes a caller is a clean 4xx
      // (or a documented 5xx, but never a leaked provider identity), not a
      // hang or an opaque failure. This branch is the evidence for whichever
      // of those is true right now.
      expect(err).toBeInstanceOf(APIError);
      const apiErr = err as APIError;
      const raw = JSON.stringify(apiErr.error ?? apiErr.message ?? "");
      expect(raw).not.toMatch(/groq|openrouter|deepseek/i);
      return;
    }

    expect(response.data?.length).toBeGreaterThanOrEqual(1);
    const image = response.data![0];
    expect(image.url ?? image.b64_json).toBeTruthy();
  });
});
