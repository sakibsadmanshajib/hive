import { describe, it, expect } from "vitest";
import OpenAI from "openai";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const authHeaders = { Authorization: `Bearer ${API_KEY}` };

const FIXTURES_DIR =
  process.env.HIVE_FIXTURES_DIR ??
  resolve(__dirname, "../../../fixtures/golden");

function loadGolden(name: string): unknown {
  return JSON.parse(readFileSync(resolve(FIXTURES_DIR, name), "utf-8"));
}

describe("List Models", () => {
  const client = new OpenAI({
    baseURL: BASE_URL,
    apiKey: API_KEY,
  });

  it("returns a valid list response via SDK", async () => {
    const response = await client.models.list();

    expect(response.object).toBe("list");
    expect(response.data).toBeInstanceOf(Array);
  });

  it("matches the golden fixture shape", async () => {
    const res = await fetch(`${BASE_URL}/models`, { headers: authHeaders });
    const body = await res.json();
    const golden = loadGolden("models-list.json") as {
      object: string;
      data: Array<{ id: string; object: string; owned_by: string }>;
    };

    // Top-level envelope must match exactly.
    expect(body.object).toBe(golden.object);

    // Each golden entry must appear in the live response with the same
    // structural fields. We do not compare `created` — that timestamp
    // changes per deploy and carries no API-surface meaning.
    for (const expected of golden.data) {
      const actual = body.data.find(
        (m: { id: string }) => m.id === expected.id
      );
      expect(actual, `missing model ${expected.id}`).toBeDefined();
      expect(actual).toEqual(
        expect.objectContaining({
          id: expected.id,
          object: expected.object,
          owned_by: expected.owned_by,
        })
      );
      expect(typeof actual.created).toBe("number");
    }
  });

  it("returns the seeded Hive aliases without provider leaks", async () => {
    const res = await fetch(`${BASE_URL}/models`, { headers: authHeaders });
    const body = await res.json();
    const ids = body.data.map((model: { id: string }) => model.id);

    expect(ids).toEqual(expect.arrayContaining(["hive-default", "hive-fast", "hive-auto"]));
    expect(JSON.stringify(body)).not.toMatch(/openrouter|groq/i);
  });
});
