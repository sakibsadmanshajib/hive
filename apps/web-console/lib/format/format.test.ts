import { describe, expect, it } from "vitest";

import {
  CREDITS_PER_USD,
  formatCredits,
  formatLatencyMs,
  formatShortDate,
  formatUsdBalanceFromCredits,
  SUB_CENT_BALANCE,
} from "./credits";
import { formatDateTime, formatLongDate } from "./datetime";
import { formatCurrency, formatTakaSubunits } from "./money";
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
 * Issue #1332: the dashboard printed the balance as a bare integer while the
 * API keys table printed the same quantity in dollars. The console settled on
 * dollars, and a balance is spendable money, so this formatter truncates
 * where the pricing formatter rounds.
 */
describe("formatUsdBalanceFromCredits", () => {
  it("renders one dollar per billion credits", () => {
    expect(formatUsdBalanceFromCredits(CREDITS_PER_USD)).toBe("$1.00");
  });

  it("never rounds a balance up to money the customer does not hold", () => {
    expect(formatUsdBalanceFromCredits(99_996_364_207)).toBe("$99.99");
  });

  it("truncates in credits, so a float product cannot shave a cent off a real balance", () => {
    // 8.29 times 100 is 828.9999999999999 in IEEE 754, so truncating in
    // dollars would print $8.28 here.
    expect(formatUsdBalanceFromCredits(8_290_000_000)).toBe("$8.29");
    expect(formatUsdBalanceFromCredits(1_230_000_000)).toBe("$1.23");
  });

  it("keeps a sub-cent balance visible instead of printing zero", () => {
    // A bound rather than a figure. It used to print the exact amount at up
    // to nine decimals, which is the width of one credit: "$0.000000858" is
    // arithmetically true and carries nothing a reader can act on, and it sat
    // in chrome the customer cannot dismiss. What the nine decimals were
    // there to prevent still holds, since the bound is not the string an
    // empty wallet renders.
    expect(formatUsdBalanceFromCredits(662_000)).toBe(SUB_CENT_BALANCE);
    expect(formatUsdBalanceFromCredits(858)).toBe(SUB_CENT_BALANCE);
    expect(formatUsdBalanceFromCredits(858)).not.toBe("$0.00");
  });

  it("renders a single credit, the smallest unit there is", () => {
    expect(formatUsdBalanceFromCredits(1)).toBe(SUB_CENT_BALANCE);
  });

  it("never prints more than two decimal places, at any magnitude", () => {
    // The general rule behind the two cases above, asserted across
    // magnitudes so a future precision change cannot reintroduce a
    // nine-significant-figure balance somewhere the named cases do not look.
    const samples = [
      1, 858, 999, 395_640, 499_999_999, 500_000_000, 8_290_000_000,
      99_996_364_207, 123_456_789_012, -8_295_000_000,
    ];
    for (const credits of samples) {
      const shown = formatUsdBalanceFromCredits(credits);
      if (shown === SUB_CENT_BALANCE) {
        continue;
      }
      expect(shown).toMatch(/^-?\$[\d,]+\.\d{2}$/);
    }
  });

  it("never renders more dollars than the balance holds, at any magnitude", () => {
    // The floor property PR #1343 and PR #1346 established, re-pinned against
    // the precision change: a displayed balance is never above the real one.
    const samples = [
      1, 858, 999, 395_640, 499_999_999, 500_000_000, 8_290_000_000,
      9_996_364_207, 99_996_364_207, 123_456_789_012,
    ];
    for (const credits of samples) {
      const rendered = formatUsdBalanceFromCredits(credits);
      if (rendered === SUB_CENT_BALANCE) {
        // The bound claims only that the balance is under a cent, which is a
        // weaker claim than any figure, so it cannot overstate.
        expect(credits / CREDITS_PER_USD).toBeLessThan(0.01);
        continue;
      }
      const shown = Number(rendered.replace(/[$,]/g, ""));
      expect(shown).toBeLessThanOrEqual(credits / CREDITS_PER_USD);
    }
  });

  it("rounds a negative balance down, so an overdrawn account is not flattered", () => {
    // available_credits is posted minus reserved, so a workspace whose holds
    // exceed its posted credits reads negative. Rounding toward zero would
    // show less of the hole than is there.
    expect(formatUsdBalanceFromCredits(-8_295_000_000)).toBe("-$8.30");
  });

  it("renders an empty balance as zero dollars, not as an absence", () => {
    expect(formatUsdBalanceFromCredits(0)).toBe("$0.00");
  });

  it("renders a non-finite value as an absence, never as an empty wallet", () => {
    expect(formatUsdBalanceFromCredits(Number.NaN)).toBe("—");
    expect(formatUsdBalanceFromCredits(Number.POSITIVE_INFINITY)).toBe("—");
  });
});
