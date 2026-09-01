import { describe, expect, it, vi, afterEach } from "vitest";

describe("chat-link", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("builds a model preselect URL on the default chat origin", async () => {
    vi.resetModules();
    const { chatModelUrl } = await import("@/lib/chat-link");
    expect(chatModelUrl("groq/llama-3.3-70b")).toBe(
      "https://chat-hive.scubed.co/?model=groq%2Fllama-3.3-70b",
    );
  });

  it("encodes reserved characters in model ids", async () => {
    vi.resetModules();
    const { chatModelUrl } = await import("@/lib/chat-link");
    expect(chatModelUrl("a/b:c")).toBe(
      "https://chat-hive.scubed.co/?model=a%2Fb%3Ac",
    );
  });

  it("honors the NEXT_PUBLIC_CHAT_URL override", async () => {
    vi.stubEnv("NEXT_PUBLIC_CHAT_URL", "http://localhost:8080");
    vi.resetModules();
    const { chatModelUrl } = await import("@/lib/chat-link");
    expect(chatModelUrl("m1")).toBe("http://localhost:8080/?model=m1");
  });

  it("strips a trailing slash from a pasted NEXT_PUBLIC_CHAT_URL override", async () => {
    vi.stubEnv("NEXT_PUBLIC_CHAT_URL", "http://localhost:8080/");
    vi.resetModules();
    const { chatModelUrl } = await import("@/lib/chat-link");
    expect(chatModelUrl("m1")).toBe("http://localhost:8080/?model=m1");
  });
});

describe("isChatCapable", () => {
  it("accepts the chat badge the seeded chat aliases carry", async () => {
    const { isChatCapable } = await import("@/lib/chat-link");
    // The exact arrays seeded for hive-small, deepseek-v4-pro, hive-free and
    // hive-auto (supabase/migrations).
    expect(isChatCapable(["stable", "chat", "responses"])).toBe(true);
    expect(
      isChatCapable(["stable", "chat", "responses", "tools", "reasoning"]),
    ).toBe(true);
    expect(isChatCapable(["stable", "chat", "responses", "task-aware"])).toBe(
      true,
    );
  });

  it("refuses the embedding, speech-to-text and text-to-speech aliases", async () => {
    const { isChatCapable } = await import("@/lib/chat-link");
    expect(isChatCapable(["stable", "embeddings"])).toBe(false);
    expect(isChatCapable(["voice", "stt"])).toBe(false);
    expect(isChatCapable(["voice", "tts"])).toBe(false);
    expect(isChatCapable([])).toBe(false);
  });

  it("reads the badge case-insensitively and ignores stray whitespace", async () => {
    const { isChatCapable } = await import("@/lib/chat-link");
    expect(isChatCapable(["Chat"])).toBe(true);
    expect(isChatCapable([" chat "])).toBe(true);
    // A badge that merely contains the word is not the chat capability.
    expect(isChatCapable(["chatty"])).toBe(false);
  });
});
