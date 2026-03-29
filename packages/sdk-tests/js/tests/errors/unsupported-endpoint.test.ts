import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";

describe("Unsupported endpoint errors", () => {
  const client = new OpenAI({
    baseURL: BASE_URL,
    apiKey: "test-key",
  });

  it("chat.completions.create throws NotFoundError with planned status", async () => {
    try {
      await client.chat.completions.create({
        model: "gpt-4o",
        messages: [{ role: "user", content: "hello" }],
      });
      expect.fail("Expected NotFoundError to be thrown");
    } catch (err) {
      expect(err).toBeInstanceOf(OpenAI.NotFoundError);

      const notFound = err as OpenAI.NotFoundError;
      expect(notFound.status).toBe(404);

      // The SDK parses the JSON body and stores the inner error object directly
      // Response JSON: {"error":{"message":"...","type":"...","param":null,"code":"..."}}
      // SDK unwraps to: notFound.error = {"message":"...","type":"...","param":null,"code":"..."}
      const body = notFound.error as Record<string, unknown> | undefined;
      expect(body?.type).toBe("unsupported_endpoint");
      expect(body?.code).toBe("endpoint_not_available");

      const message = body?.message as string;
      expect(message).toContain("planned but not yet available");

      // Provider-blind: no mention of provider, upstream, or OpenAI
      expect(message).not.toMatch(/provider/i);
      expect(message).not.toMatch(/upstream/i);
      expect(message).not.toMatch(/openai/i);
    }
  });

  it("fine_tuning.jobs.create throws NotFoundError with unsupported status", async () => {
    try {
      await client.fineTuning.jobs.create({
        model: "gpt-4o",
        training_file: "file-abc123",
      });
      expect.fail("Expected NotFoundError to be thrown");
    } catch (err) {
      expect(err).toBeInstanceOf(OpenAI.NotFoundError);

      const notFound = err as OpenAI.NotFoundError;
      expect(notFound.status).toBe(404);

      const body = notFound.error as Record<string, unknown> | undefined;
      expect(body?.type).toBe("unsupported_endpoint");
      expect(body?.code).toBe("endpoint_unsupported");
    }
  });
});
