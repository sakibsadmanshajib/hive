import { describe, expect, it } from "vitest";

import { blendedPriceNote } from "./blended-price-note";

describe("blendedPriceNote", () => {
  it("names the unit so a nine-figure credits number cannot read as a balance", () => {
    // Issue #1408. The tile sits on a page whose sibling surface shows an
    // account balance of 99,996,364,207 credits, so a bare "161,813,971
    // credits" under a rate tile reads as money held rather than as a rate.
    const note = blendedPriceNote({
      kind: "ok",
      creditsPerMillion: 161_813_971.499,
      windowUnsupportedNote: undefined,
    });

    expect(note).toContain("161,813,971 credits per 1M tokens");
  });

  it("prints a whole number of credits, never a fractional one", () => {
    // Credits are integers everywhere else in the product, and Intl's
    // default of three fraction digits was what produced ".499" here.
    const note = blendedPriceNote({
      kind: "ok",
      creditsPerMillion: 161_813_971.499,
      windowUnsupportedNote: undefined,
    });

    expect(note).not.toMatch(/\d\.\d/);
  });

  it("says nothing was served rather than showing a zero rate", () => {
    expect(
      blendedPriceNote({
        kind: "no-tokens",
        creditsPerMillion: null,
        windowUnsupportedNote: undefined,
      })
    ).toBe("No tokens served in this window.");
  });

  it("passes the window note through when the window has no comparison", () => {
    expect(
      blendedPriceNote({
        kind: "window-unsupported",
        creditsPerMillion: null,
        windowUnsupportedNote: "Custom windows carry no prior period.",
      })
    ).toBe("Custom windows carry no prior period.");
  });

  it("does not print a rate it does not have", () => {
    const note = blendedPriceNote({
      kind: "ok",
      creditsPerMillion: null,
      windowUnsupportedNote: undefined,
    });

    expect(note).not.toContain("0 credits per 1M tokens");
  });
});
