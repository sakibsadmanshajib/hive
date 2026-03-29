import { describe, it, expect } from "vitest";
import OpenAI from "openai";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";

function loadGolden(name: string): unknown {
  const filePath = resolve(__dirname, "../../../../fixtures/golden", name);
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
});
