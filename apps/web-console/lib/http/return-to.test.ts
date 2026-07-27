/**
 * Redirect-target contract for the locale switcher route.
 *
 * A raw string prefix test on `return_to` was bypassable, so these cases pin
 * the parse-then-validate behaviour: anything that does not normalize to a
 * /console path on this origin falls back to /console.
 */

import { describe, it, expect } from "vitest";

import { resolveReturnTo } from "./return-to";

const ORIGIN = "https://console.hivegpt.io";

describe("resolveReturnTo", () => {
  it("keeps console paths, including nested ones and query strings", () => {
    expect(resolveReturnTo("/console", ORIGIN)).toBe("/console");
    expect(resolveReturnTo("/console/billing", ORIGIN)).toBe("/console/billing");
    expect(resolveReturnTo("/console/analytics?range=7d", ORIGIN)).toBe(
      "/console/analytics?range=7d",
    );
  });

  it("rejects traversal that normalizes outside /console", () => {
    // The bug this test exists for: starts with "/console", resolves to "/settings".
    expect(resolveReturnTo("/console/../settings", ORIGIN)).toBe("/console");
    expect(resolveReturnTo("/console/../../etc/passwd", ORIGIN)).toBe("/console");
  });

  it("keeps an encoded separator inside /console rather than escaping it", () => {
    expect(resolveReturnTo("/console/..%2Fsettings", ORIGIN)).toBe(
      "/console/..%2Fsettings",
    );
  });

  it("rejects other origins", () => {
    expect(resolveReturnTo("//evil.test/console", ORIGIN)).toBe("/console");
    expect(resolveReturnTo("https://evil.test/console", ORIGIN)).toBe("/console");
    expect(resolveReturnTo("http://evil.test/console", ORIGIN)).toBe("/console");
  });

  it("rejects paths that merely share the /console prefix", () => {
    expect(resolveReturnTo("/consoleXY", ORIGIN)).toBe("/console");
    expect(resolveReturnTo("/settings", ORIGIN)).toBe("/console");
  });

  it("drops fragments rather than passing them to Location", () => {
    expect(resolveReturnTo("/console/billing#token", ORIGIN)).toBe(
      "/console/billing",
    );
  });

  it("falls back for missing or non-string input", () => {
    expect(resolveReturnTo(null, ORIGIN)).toBe("/console");
    expect(resolveReturnTo("", ORIGIN)).toBe("/console");
  });
});
