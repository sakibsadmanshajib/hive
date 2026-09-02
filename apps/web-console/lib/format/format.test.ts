import { describe, expect, it } from "vitest";

import {
  formatCreditAmount,
  formatCreditCount,
  formatCreditDigits,
  formatCredits,
  formatLatencyMs,
  formatShortDate,
} from "./credits";
import { formatDateTime, formatLongDate } from "./datetime";
import { formatCurrency, formatTakaSubunits } from "./money";
import { CURRENCY_MARK } from "@/tests/support/currency-mark";
import { intlTag, resolveLocale } from "@/lib/i18n/locales";

describe("resolveLocale", () => {
  it("accepts supported locales and rejects everything else", () => {
    expect(resolveLocale("bn")).toBe("bn");
    expect(resolveLocale("en")).toBe("en");
    expect(resolveLocale("fr")).toBe("en");
    expect(resolveLocale("../../etc/passwd")).toBe("en");
    expect(resolveLocale(undefined)).toBe("en");
  });
});

describe("intlTag", () => {
  // bn-BD on its own resolves to numberingSystem "beng", which would render
  // credit balances in Bengali digits. The -u-nu-latn extension is what keeps
  // money legible, so assert it never gets dropped.
  it("pins Bengali to Latin digits", () => {
    expect(intlTag("bn", "number")).toBe("bn-BD-u-nu-latn");
    expect(intlTag("bn", "date")).toBe("bn-BD-u-nu-latn");
    expect(intlTag("bn", "grouping")).toBe("bn-BD-u-nu-latn");
    expect(
      new Intl.NumberFormat(intlTag("bn", "number")).resolvedOptions()
        .numberingSystem,
    ).toBe("latn");
  });
});

describe("formatCredits", () => {
  it("defaults to English thousand grouping", () => {
    expect(formatCredits(1234567)).toBe("1,234,567");
  });

  it("uses lakh/crore grouping with Latin digits for Bengali", () => {
    expect(formatCredits(1234567, "bn")).toBe("12,34,567");
  });

  it("clamps non-finite input", () => {
    expect(formatCredits(Number.NaN)).toBe("0");
  });
});

describe("formatCreditCount", () => {
  it("groups a wire string without going through Number", () => {
    expect(formatCreditCount("524653338")).toBe("524,653,338");
  });

  it("keeps full precision past Number.MAX_SAFE_INTEGER", () => {
    // 2^53 + 1: a Number round trip renders this as ...992, which on a money
    // surface is a figure the ledger never held.
    expect(formatCreditCount("9007199254740993")).toBe("9,007,199,254,740,993");
  });

  it("renders an unrecorded quantity as absent, not as zero", () => {
    expect(formatCreditCount(null)).toBe("\u2014");
    expect(formatCreditCount("")).toBe("\u2014");
    expect(formatCreditCount("not-a-number")).toBe("\u2014");
    expect(formatCreditCount("0")).toBe("0");
  });

  it("uses lakh/crore grouping for Bengali", () => {
    expect(formatCreditCount("1234567", "bn")).toBe("12,34,567");
  });
});

describe("formatLatencyMs", () => {
  it("renders sub-second values in ms", () => {
    expect(formatLatencyMs(340)).toBe("340ms");
    expect(formatLatencyMs(0)).toBe("0ms");
  });

  it("renders one second and above in seconds", () => {
    expect(formatLatencyMs(1200)).toBe("1.2s");
    expect(formatLatencyMs(18450)).toBe("18.5s");
  });

  it("never prints a 1000ms that should have been one second", () => {
    // The unit is chosen from the rounded value, so nothing between
    // 999.5 and 1000 can slip out of the millisecond branch as "1000ms".
    expect(formatLatencyMs(999.7)).toBe("1.0s");
    expect(formatLatencyMs(999.4)).toBe("999ms");
  });

  it("renders an em-dash for a genuinely unknown latency, never a fabricated zero", () => {
    expect(formatLatencyMs(null)).toBe("—");
  });

  it("clamps non-finite and negative input to the em-dash", () => {
    expect(formatLatencyMs(Number.NaN)).toBe("—");
    expect(formatLatencyMs(-5)).toBe("—");
  });
});

describe("date formatting", () => {
  // 2026-07-27T18:30:00Z is 2026-07-28T00:30 in Dhaka — the assertions below
  // fail if the Asia/Dhaka pin is ever dropped.
  const iso = "2026-07-27T18:30:00Z";

  it("renders day-first short dates in Dhaka time", () => {
    expect(formatShortDate(iso)).toBe("28 Jul 2026");
  });

  it("renders Bengali month names with Latin digits", () => {
    expect(formatShortDate(iso, "bn")).toContain("2026");
    expect(formatShortDate(iso, "bn")).toContain("28");
  });

  it("returns an em-dash for empty values", () => {
    expect(formatShortDate(null)).toBe("—");
  });

  it("passes unparseable values through", () => {
    expect(formatDateTime("not-a-date")).toBe("not-a-date");
    expect(formatLongDate("not-a-date")).toBe("not-a-date");
  });
});

describe("money formatting", () => {
  it("renders the rail currency without a USD fallback", () => {
    expect(formatCurrency(120000, "BDT")).toContain("1,200.00");
  });

  it("keeps BigInt precision and grouping beyond MAX_SAFE_INTEGER", () => {
    expect(formatTakaSubunits("100050")).toBe("৳1,000.50");
    expect(formatTakaSubunits("9007199254740993000")).toBe(
      "৳90,071,992,547,409,930.00",
    );
  });

  it("groups taka by lakh/crore for Bengali", () => {
    expect(formatTakaSubunits("100050", "bn")).toBe("৳1,000.50");
    expect(formatTakaSubunits("12345678900", "bn")).toBe("৳12,34,56,789.00");
  });

  it("floors negative and unparseable totals", () => {
    expect(formatTakaSubunits("-1")).toBe("৳0.00");
    expect(formatTakaSubunits("abc")).toBe("৳0.00");
  });
});

/**
 * The guard for issue #1694, at the formatter.
 *
 * Every balance, usage and spend surface renders a credit quantity through
 * formatCreditAmount, and a currency figure appears only on an invoice (owner
 * ruling, .wolf/decisions.md D-070). What this file pins is that the function
 * those surfaces call cannot emit a currency figure at all: the leak returns
 * the moment somebody routes a credit quantity through Intl's currency style
 * again, and a test that only checked one balance renders "12,345 credits"
 * would go on passing while a second, dollar-denominated line was added
 * beside it.
 */
describe("formatCreditAmount", () => {
  it("renders the exact credit count and its unit, with no currency at all", () => {
    // The workspace balance observed live on the demo box, 2026-08-29. It used
    // to render "$99.99", from which a customer who had paid a known price for
    // a known credit grant could read the peg straight off.
    expect(formatCreditAmount(99_996_364_207)).toBe("99,996,364,207 credits");
    expect(formatCreditAmount(1_000_000_000)).toBe("1,000,000,000 credits");
  });

  it("never emits a currency mark, at any magnitude or sign", () => {
    const samples = [
      0, 1, 858, 999, 395_640, 499_999_999, 500_000_000, 8_290_000_000,
      99_996_364_207, 123_456_789_012, Number.MAX_SAFE_INTEGER,
      -1, -8_295_000_000, Number.NaN, Number.POSITIVE_INFINITY,
    ];
    for (const credits of samples) {
      expect(formatCreditAmount(credits)).not.toMatch(CURRENCY_MARK);
    }
  });

  it("is exact, so no balance is ever overstated or rounded to nothing", () => {
    // The whole class of defect the two currency formatters kept producing
    // (#1344, #1345): one floored to cents, one rounded to nearest, and a
    // sub-cent figure had to be replaced by a bound because it would otherwise
    // render as an empty wallet. An integer count of credits has no such case.
    expect(formatCreditAmount(1)).toBe("1 credit");
    expect(formatCreditAmount(858)).toBe("858 credits");
    expect(formatCreditAmount(395_640)).toBe("395,640 credits");
  });

  it("renders an empty balance as zero credits, not as an absence", () => {
    expect(formatCreditAmount(0)).toBe("0 credits");
  });

  it("renders a negative balance in full, so an overdrawn account is not flattered", () => {
    // available_credits is posted minus reserved, so a workspace whose holds
    // exceed its posted credits reads negative.
    expect(formatCreditAmount(-8_295_000_000)).toBe("-8,295,000,000 credits");
    expect(formatCreditAmount(-1)).toBe("-1 credit");
  });

  it("renders a non-finite value as an absence, never as an empty wallet", () => {
    expect(formatCreditAmount(Number.NaN)).toBe("—");
    expect(formatCreditAmount(Number.POSITIVE_INFINITY)).toBe("—");
  });

  it("truncates a fractional value rather than inventing a credit", () => {
    // Credits are whole in storage, so a fraction here is a decode artefact.
    expect(formatCreditAmount(1.9)).toBe("1 credit");
    expect(formatCreditAmount(2.9)).toBe("2 credits");
  });
});

describe("formatCreditDigits", () => {
  it("is formatCreditAmount without the unit word, for a stated pair", () => {
    // The API keys budget cell reads "9,000,000,000 of 10,000,000,000
    // credits/mo": one unit word for the pair, and both halves grouped the
    // same way so they cannot read as two different scales.
    expect(formatCreditDigits(9_000_000_000)).toBe("9,000,000,000");
    expect(`${formatCreditDigits(9_000_000_000)} of ${formatCreditAmount(10_000_000_000)}`).toBe(
      "9,000,000,000 of 10,000,000,000 credits",
    );
  });

  it("never emits a currency mark", () => {
    for (const credits of [0, 1, 858, 8_290_000_000, -1, Number.NaN]) {
      expect(formatCreditDigits(credits)).not.toMatch(CURRENCY_MARK);
    }
  });

  it("renders a non-finite value as an absence", () => {
    expect(formatCreditDigits(Number.NaN)).toBe("—");
  });
});
