import { describe, expect, it } from "vitest";

import { formatCredits, formatLatencyMs, formatShortDate } from "./credits";
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
