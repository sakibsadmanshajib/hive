import { describe, it, expect, afterAll } from "vitest";
import OpenAI, { APIError, toFile } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";
const MODEL = process.env.HIVE_TEST_MODEL ?? "hive-free";

// Known Issues item 4 (CLAUDE.md): the success path (status=completed) is not
// exercisable with the current provider mix — neither OpenRouter nor Groq
// exposes a native batch API for LiteLLM's managed file upload, and
// Phase 15's local batch executor is what serves /v1/batches at all. This
// suite therefore asserts the submitter and cancel paths, which are known to
// work end to end, and does not wait for or assert completion.
describe("Batches", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });
  const fileIds: string[] = [];
  const batchIds: string[] = [];

  afterAll(async () => {
    for (const id of batchIds) {
      await client.batches.cancel(id).catch(() => undefined);
    }
    for (const id of fileIds) {
      await client.files.delete(id).catch(() => undefined);
    }
  });

  it("submits a batch, retrieves it, then cancels it", async () => {
    const requestLine = JSON.stringify({
      custom_id: "sdk-conformance-1",
      method: "POST",
      url: "/v1/chat/completions",
      body: {
        model: MODEL,
        messages: [{ role: "user", content: "Say hi" }],
        max_tokens: 16,
      },
    });

    const uploaded = await client.files.create({
      file: await toFile(Buffer.from(requestLine + "\n", "utf-8"), "batch-input.jsonl", {
        type: "application/jsonl",
      }),
      purpose: "batch",
    });
    fileIds.push(uploaded.id);

    const batch = await client.batches.create({
      input_file_id: uploaded.id,
      endpoint: "/v1/chat/completions",
      completion_window: "24h",
    });
    batchIds.push(batch.id);

    expect(batch.object).toBe("batch");
    expect(batch.id).toBeTruthy();
    expect(batch.input_file_id).toBe(uploaded.id);
    // Not asserting a specific status: the local batch executor's actual
    // processing speed is an implementation detail, not a gateway contract.
    // The contract under test is that the status is one of the wire enum's
    // real values, not a garbage or empty string.
    expect([
      "validating",
      "failed",
      "in_progress",
      "finalizing",
      "completed",
      "expired",
      "cancelling",
      "cancelled",
    ]).toContain(batch.status);

    const retrieved = await client.batches.retrieve(batch.id);
    expect(retrieved.id).toBe(batch.id);

    const list = await client.batches.list();
    expect(list.data.some((b) => b.id === batch.id)).toBe(true);

    // Cancel is best-effort here: if the local executor already reached a
    // terminal state (completed/failed/expired) before this call lands, a
    // cancel of an already-terminal batch may itself be a clean 4xx rather
    // than a state change, and that is a valid outcome too.
    try {
      const cancelled = await client.batches.cancel(batch.id);
      expect(cancelled.id).toBe(batch.id);
    } catch (err) {
      expect(err).toBeInstanceOf(APIError);
      const apiErr = err as APIError;
      expect(apiErr.status).toBeGreaterThanOrEqual(400);
      expect(apiErr.status).toBeLessThan(500);
    }
  });

  it("rejects an unsupported batch endpoint value with a structured 4xx error", async () => {
    const uploaded = await client.files.create({
      file: await toFile(Buffer.from("{}\n", "utf-8"), "bad-batch-input.jsonl", {
        type: "application/jsonl",
      }),
      purpose: "batch",
    });
    fileIds.push(uploaded.id);

    await expect(
      client.batches.create({
        input_file_id: uploaded.id,
        // @ts-expect-error -- not one of the five endpoint values any OpenAI-
        // compatible batch API recognizes, deliberately, so this assertion
        // holds regardless of which of those five this catalog can serve.
        endpoint: "/v1/not-a-real-endpoint",
        completion_window: "24h",
      }),
    ).rejects.toMatchObject({ status: 400 });
  });
});
