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

  // Issue #1319 is fixed: the empty 200 is gone, proven live on the deployed
  // box, where the guard logged the upstream empty data array and answered a
  // provider-blind 502 instead of a success (issue #1382 carries the log line).
  //
  // The route still cannot produce an image, so this call takes the error
  // branch below rather than the success branch. maxRetries is 0 deliberately:
  // a 5xx is retryable by definition and this SDK retries twice by default,
  // but a route whose capability flag is a carried-forward legacy flag can
  // never succeed on a retry. Measured on run 33240963131, the first attempt
  // refused correctly in about four seconds and the test still hit its 60
  // second ceiling because the later attempts hung upstream. One attempt is
  // what this test is actually about.
  //
  // The success assertions moved OUT of the try block, and that is a fix in
  // its own right, not cosmetics: a failed expect() is itself a thrown
  // exception, so inside the try the catch below caught this suite own
  // assertion and re-reported it as "not an APIError". The real defect was
  // invisible behind a confusing message about instanceof.
  it("images.generate either returns a real image or fails with a structured, provider-blind error (never an empty success)", async () => {
    let response: Awaited<ReturnType<typeof client.images.generate>>;
    try {
      response = await client.images.generate(
        {
          model: IMAGE_MODEL,
          prompt: "a single red circle on a white background",
          n: 1,
        },
        { maxRetries: 0 },
      );
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
