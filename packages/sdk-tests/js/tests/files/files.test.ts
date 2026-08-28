import { describe, it, expect, afterAll } from "vitest";
import OpenAI, { toFile } from "openai";

const BASE_URL = process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1";
const API_KEY = process.env.HIVE_API_KEY ?? "test-key";

describe("Files", () => {
  const client = new OpenAI({ baseURL: BASE_URL, apiKey: API_KEY });
  const uploadedIds: string[] = [];

  afterAll(async () => {
    // Best-effort cleanup so a live run against the production catalog does
    // not accumulate rows across repeated invocations.
    for (const id of uploadedIds) {
      await client.files.delete(id).catch(() => undefined);
    }
  });

  it("uploads, lists, retrieves metadata, downloads content, then deletes a file", async () => {
    const body = "sdk-conformance-suite test file\nline two\n";
    const uploaded = await client.files.create({
      file: await toFile(Buffer.from(body, "utf-8"), "sdk-conformance.txt", {
        type: "text/plain",
      }),
      purpose: "assistants",
    });
    uploadedIds.push(uploaded.id);

    expect(uploaded.object).toBe("file");
    expect(uploaded.id).toBeTruthy();
    expect(uploaded.purpose).toBe("assistants");
    expect(uploaded.bytes).toBe(Buffer.byteLength(body, "utf-8"));

    const retrieved = await client.files.retrieve(uploaded.id);
    expect(retrieved.id).toBe(uploaded.id);
    expect(retrieved.filename).toBe("sdk-conformance.txt");

    const list = await client.files.list();
    expect(list.data.some((f) => f.id === uploaded.id)).toBe(true);

    const content = await client.files.content(uploaded.id);
    const downloaded = await content.text();
    expect(downloaded).toBe(body);

    const deleted = await client.files.delete(uploaded.id);
    expect(deleted.deleted).toBe(true);
    uploadedIds.splice(uploadedIds.indexOf(uploaded.id), 1);
  });

  it("rejects an invalid purpose with a structured 4xx error", async () => {
    await expect(
      client.files.create({
        file: await toFile(Buffer.from("x"), "bad.txt", { type: "text/plain" }),
        // @ts-expect-error -- deliberately invalid, asserting server-side rejection
        purpose: "not-a-real-purpose",
      }),
    ).rejects.toMatchObject({ status: 400 });
  });
});
