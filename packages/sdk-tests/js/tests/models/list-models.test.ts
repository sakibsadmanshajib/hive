import { describe, it, expect } from "vitest";
import OpenAI from "openai";
import { readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";

function loadGolden(name: string): unknown {
  // In Docker container, fixtures are at /fixtures/golden/
  // Locally, they are relative to the test file
  const containerPath = resolve("/fixtures/golden", name);
  const localPath = resolve(__dirname, "../../../../fixtures/golden", name);
  const filePath = existsSync(containerPath) ? containerPath : localPath;
  return JSON.parse(readFileSync(filePath, "utf-8"));
}

describe("List Models", () => {
  const client = new OpenAI({
    baseURL: BASE_URL,
    apiKey: "test-key",
  });

  it("returns a valid list response via SDK", async () => {
    const response = await client.models.list();

    expect(response.object).toBe("list");
    expect(response.data).toBeInstanceOf(Array);
  });

  it("matches the golden fixture shape", async () => {
    const res = await fetch(`${BASE_URL}/models`);
    const body = await res.json();
    const golden = loadGolden("models-list.json");

    expect(body).toEqual(golden);
  });

  it("returns the seeded Hive aliases without provider leaks", async () => {
    const res = await fetch(`${BASE_URL}/models`);
    const body = await res.json();
    const ids = body.data.map((model: { id: string }) => model.id);

    expect(ids).toEqual(expect.arrayContaining(["hive-default", "hive-fast", "hive-auto"]));
    expect(JSON.stringify(body)).not.toMatch(/openrouter|groq/i);
  });
});
