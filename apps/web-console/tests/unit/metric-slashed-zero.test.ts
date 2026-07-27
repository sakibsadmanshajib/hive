import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

// A slashed zero in a credit, usage, or invoice figure is a requirement, not a
// trait borrowed from whichever mono the console happens to load. Geist Mono
// draws one by default today, so dropping the declaration would look harmless
// in review and only surface as an O-shaped zero after a family swap or a
// fallback render.
const metricRule = (() => {
  const css = readFileSync(
    resolve(__dirname, "../../app/globals.css"),
    "utf8",
  );
  const match = css.match(/\.metric\s*\{([^}]*)\}/);
  if (!match) throw new Error("`.metric` rule not found in app/globals.css");
  return match[1];
})();

describe(".metric", () => {
  it("asks for a slashed zero and keeps tabular figures", () => {
    expect(metricRule).toMatch(
      /font-variant-numeric:\s*slashed-zero\s+tabular-nums;/,
    );
  });

  it("sets the same features at the OpenType level", () => {
    expect(metricRule).toMatch(/font-feature-settings:[^;]*"tnum"/);
    expect(metricRule).toMatch(/font-feature-settings:[^;]*"zero"/);
  });
});
