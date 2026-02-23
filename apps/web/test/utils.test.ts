import { describe, expect, it } from "vitest";

import { cn } from "../src/lib/utils";

describe("cn", () => {
  it("merges conditional class names", () => {
    expect(cn("a", false && "b", "c")).toBe("a c");
  });
});
