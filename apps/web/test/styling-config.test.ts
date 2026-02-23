import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

describe("styling config", () => {
  it("has tailwind and postcss config files", () => {
    expect(existsSync(resolve(process.cwd(), "tailwind.config.ts"))).toBe(true);
    expect(existsSync(resolve(process.cwd(), "postcss.config.js"))).toBe(true);
  });
});
