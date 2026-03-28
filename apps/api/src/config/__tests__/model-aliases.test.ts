import { describe, it, expect } from "vitest";
import { resolveModelAlias, MODEL_ALIASES } from "../model-aliases";

describe("DIFF-03: Model alias resolution", () => {
  it("resolves gpt-3.5-turbo to gpt-4o-mini", () => {
    expect(resolveModelAlias("gpt-3.5-turbo")).toBe("gpt-4o-mini");
  });

  it("resolves gpt-4 to gpt-4o", () => {
    expect(resolveModelAlias("gpt-4")).toBe("gpt-4o");
  });

  it("resolves gpt-4-turbo to gpt-4o", () => {
    expect(resolveModelAlias("gpt-4-turbo")).toBe("gpt-4o");
  });

  it("resolves text-embedding-ada-002 to text-embedding-3-small", () => {
    expect(resolveModelAlias("text-embedding-ada-002")).toBe("text-embedding-3-small");
  });

  it("passes through text-embedding-3-small unchanged (first-class ID, not aliased)", () => {
    expect(resolveModelAlias("text-embedding-3-small")).toBe("text-embedding-3-small");
  });

  it("passes through gpt-4o unchanged (first-class ID, not aliased)", () => {
    expect(resolveModelAlias("gpt-4o")).toBe("gpt-4o");
  });

  it("passes through gpt-4o-mini unchanged (first-class ID, not aliased)", () => {
    expect(resolveModelAlias("gpt-4o-mini")).toBe("gpt-4o-mini");
  });

  it("passes through unknown model names unchanged", () => {
    expect(resolveModelAlias("claude-sonnet-4-20250514")).toBe("claude-sonnet-4-20250514");
    expect(resolveModelAlias("some-future-model")).toBe("some-future-model");
  });

  it("MODEL_ALIASES does not contain first-class model IDs as keys", () => {
    // First-class model IDs must NOT be alias keys.
    expect(MODEL_ALIASES).not.toHaveProperty("gpt-4o");
    expect(MODEL_ALIASES).not.toHaveProperty("gpt-4o-mini");
    expect(MODEL_ALIASES).not.toHaveProperty("text-embedding-3-small");
  });

  it("MODEL_ALIASES has at least 3 entries", () => {
    expect(Object.keys(MODEL_ALIASES).length).toBeGreaterThanOrEqual(3);
  });
});
