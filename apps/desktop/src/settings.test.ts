import { describe, expect, it } from "vitest";

import { validateServerUrl } from "./settings";

describe("validateServerUrl", () => {
  it("rejects empty input", () => {
    expect(validateServerUrl("").ok).toBe(false);
  });

  it("rejects whitespace-only input", () => {
    expect(validateServerUrl("   ").ok).toBe(false);
  });

  it("rejects missing scheme", () => {
    expect(validateServerUrl("hive.example.com").ok).toBe(false);
  });

  it("rejects javascript: scheme", () => {
    expect(validateServerUrl("javascript:alert(1)").ok).toBe(false);
  });

  it("rejects ftp: scheme", () => {
    expect(validateServerUrl("ftp://hive.example.com").ok).toBe(false);
  });

  it("rejects a URL without a host", () => {
    // WHATWG URL parsing throws outright for a special scheme (http/https)
    // with no authority, rather than yielding an empty hostname.
    expect(validateServerUrl("https://").ok).toBe(false);
  });

  it("accepts https and appends the console base path", () => {
    expect(validateServerUrl("https://hive.example.com")).toEqual({
      ok: true,
      previewUrl: "https://hive.example.com/agent-workspace",
    });
  });

  it("accepts http for local dev servers", () => {
    expect(validateServerUrl("http://localhost:8090")).toEqual({
      ok: true,
      previewUrl: "http://localhost:8090/agent-workspace",
    });
  });

  it("strips a user-provided path, query, and fragment", () => {
    expect(
      validateServerUrl("https://hive.example.com/some/path?x=1#frag")
    ).toEqual({
      ok: true,
      previewUrl: "https://hive.example.com/agent-workspace",
    });
  });

  it("trims surrounding whitespace", () => {
    expect(validateServerUrl("  https://hive.example.com  ")).toEqual({
      ok: true,
      previewUrl: "https://hive.example.com/agent-workspace",
    });
  });

  it("preserves an explicit port", () => {
    expect(validateServerUrl("https://hive.example.com:8443/")).toEqual({
      ok: true,
      previewUrl: "https://hive.example.com:8443/agent-workspace",
    });
  });
});
