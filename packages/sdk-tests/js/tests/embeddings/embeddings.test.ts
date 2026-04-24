import { describe, it, expect } from "vitest";
import OpenAI from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const EMBEDDING_MODEL =
  process.env.HIVE_EMBEDDING_MODEL ?? "hive-embedding-default";

describe("Embeddings", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });

  it("returns valid embeddings via SDK", async () => {
    const response = await client.embeddings.create({
      model: EMBEDDING_MODEL,
      input: "Hello world",
    });

    expect(response.object).toBe("list");
    expect(response.data.length).toBe(1);
    expect(response.data[0].object).toBe("embedding");
    expect(Array.isArray(response.data[0].embedding)).toBe(true);
    expect((response.data[0].embedding as number[]).length).toBeGreaterThan(0);
    expect(response.usage.prompt_tokens).toBeGreaterThan(0);
  });

  it("supports batch input", async () => {
    const response = await client.embeddings.create({
      model: EMBEDDING_MODEL,
      input: ["Hello", "World"],
    });

    expect(response.object).toBe("list");
    expect(response.data.length).toBe(2);
    expect(response.data[0].object).toBe("embedding");
    expect(response.data[1].object).toBe("embedding");
  });

  it("rejects invalid model", async () => {
    await expect(
      client.embeddings.create({
        model: "nonexistent-embedding-99",
        input: "Hello world",
      }),
    ).rejects.toBeDefined();
  });
});
