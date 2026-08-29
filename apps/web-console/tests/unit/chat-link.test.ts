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
