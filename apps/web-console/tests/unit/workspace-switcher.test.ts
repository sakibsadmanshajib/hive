import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const workspaceSwitcherPath = resolve(
  process.cwd(),
  "components/workspace-switcher.tsx"
);

describe("workspace switcher component boundary", () => {
  it("is marked as a client component before attaching DOM event handlers", () => {
    const source = readFileSync(workspaceSwitcherPath, "utf8");
    const firstLine = source.split("\n")[0]?.trim() ?? "";
    expect(firstLine).toBe("\"use client\";");
  });
});
