import { describe, expect, it } from "vitest";

import {
  CREDITS_PER_USD,
  formatCachePrice,
  formatInOutPrice,
  formatModelPrice,
  formatUsdFromCredits,
} from "./model-pricing";

// This is a billing surface. The whole point of these branches is that
// "deliberately free", "variable by design", "no such rate" and "we could not
// read the price" are different answers, and collapsing any pair of them
// misleads a customer pricing the gateway. Each case below is one of those
// collapses. Changing the display unit from credits to dollars changes HOW a
// number is drawn and must never change WHETHER an absence is distinguishable
// from a zero, so every case is asserted in both units.
describe("formatModelPrice", () => {
  it("prints a published rate as dollars per million tokens", () => {
    // The live deepseek-v4-flash input and output rates.
    expect(formatModelPrice(89_460_000, "fixed")).toBe("$0.0895");
    expect(formatModelPrice(178_920_000, "fixed")).toBe("$0.179");
  });

  it("still prints the exact credit integer when asked for credits", () => {
    expect(formatModelPrice(89_460_000, "fixed", "credits")).toBe("89,460,000");
    expect(formatModelPrice(178_920_000, "fixed", "credits")).toBe(
      "178,920,000",
    );
  });

  it("prints a published zero as zero, never as an absence", () => {
    // hive-default publishes cache_write_price_credits = 0
    // (supabase/migrations/20260824_02_free_pool_router.sql). Zero is a
    // decision: that dimension is not charged. Rendering it as "Unknown" or
    // "Free" would hide a real published rate behind a word.
    expect(formatModelPrice(0, "fixed")).toBe("$0");
    expect(formatModelPrice(0, "upstream_actual")).toBe("$0");
    expect(formatModelPrice(0, "fixed", "credits")).toBe("0");
  });

  it("calls a missing price on a variable alias Variable", () => {
    expect(formatModelPrice(null, "upstream_actual")).toBe("Variable");
    expect(formatModelPrice(null, "upstream_actual", "credits")).toBe(
      "Variable",
    );
  });

  it("calls a missing price on a fixed alias Unknown, not Variable", () => {
    // A fixed alias with no price means the lookup failed. Saying "Variable"
    // there would present a broken decode as a pricing model.
    expect(formatModelPrice(null, "fixed")).toBe("Unknown");
    expect(formatModelPrice(null, "")).toBe("Unknown");
    expect(formatModelPrice(null, "fixed", "credits")).toBe("Unknown");
  });
});

describe("formatCachePrice", () => {
  it("distinguishes no-cache-rate from a broken lookup and from free", () => {
    expect(formatCachePrice(null, "fixed")).toBe("—");
    expect(formatCachePrice(null, "upstream_actual")).toBe("Variable");
    expect(formatCachePrice(0, "fixed")).toBe("$0");
    expect(formatCachePrice(0, "fixed", "credits")).toBe("0");
  });

  it("renders a real cache rate rather than a dash", () => {
    // The corrected deepseek-v4-flash cache-read rate, 1/30 of its input rate
    // (supabase/migrations/20260825_02_deepseek_cache_read_price_correction.sql).
    expect(formatCachePrice(2_982_000, "fixed")).toBe("$0.00298");
  });
});

describe("formatUsdFromCredits", () => {
  it("matches the plain dollar shape a model page publishes", () => {
    expect(formatUsdFromCredits(200_000_000)).toBe("$0.20");
    expect(formatUsdFromCredits(CREDITS_PER_USD)).toBe("$1.00");
    expect(formatUsdFromCredits(12_500_000_000)).toBe("$12.50");
  });

  // The failure this guards is specific: rounding a real, tiny, non-zero rate
  // to two decimals prints "$0.00", which reads as free. That is strictly
  // worse than the raw credit integer this display replaces, because a raw
  // integer at least cannot be mistaken for zero.
  it("never rounds a non-zero rate down to zero", () => {
    // Every distinct non-zero rate the seeded catalog actually holds, read off
    // a database with the full migration chain applied:
    //
    //   select distinct v from (select input_price_credits v from model_aliases
    //     union all select output_price_credits from model_aliases
    //     union all select cache_read_price_credits from model_aliases
    //     union all select cache_write_price_credits from model_aliases) t
    //   where v is not null order by v;
    //
    // The list is led by one credit, which is not in the catalog: it is the
    // smallest amount the unit can express at all (1e-9 USD), so it is the
    // floor this rule has to hold at, not just the floor today's data reaches.
    const everyRealRate = [
      1, 10_000, 40_000, 1_000_000, 2_982_000, 4_000_000, 52_360_000,
      89_460_000, 105_000_000, 178_920_000, 210_000_000, 420_000_000,
      840_000_000, 1_570_800_000, 4_712_400_000, 30_800_000_000,
      43_166_670_000,
    ];
    for (const credits of everyRealRate) {
      const rendered = formatUsdFromCredits(credits);
      expect(rendered, `${credits} credits rendered as free`).not.toBe("$0.00");
      expect(rendered, `${credits} credits rendered as free`).not.toBe("$0");
    }
  });

  it("renders the smallest expressible rate exactly", () => {
    // One credit is 1e-9 USD, so nine decimals is the exact width of the unit.
    expect(formatUsdFromCredits(1)).toBe("$0.000000001");
    // The smallest rate the live catalog actually publishes: the
    // hive-embedding-default input rate, one hundred-thousandth of a dollar
    // per million tokens. Two decimals would print this as "$0.00".
    expect(formatUsdFromCredits(10_000)).toBe("$0.00001");
  });

  it("prints a true zero as zero", () => {
    expect(formatUsdFromCredits(0)).toBe("$0");
  });
});

describe("formatInOutPrice", () => {
  it("formats each side independently", () => {
    expect(
      formatInOutPrice({
        input_price_credits: 89_460_000,
        output_price_credits: 178_920_000,
        pricing_mode: "fixed",
      }),
    ).toBe("$0.0895 / $0.179");
  });

  it("does not let one side borrow the other side's number", () => {
    expect(
      formatInOutPrice({
        input_price_credits: null,
        output_price_credits: null,
        pricing_mode: "upstream_actual",
      }),
    ).toBe("Variable / Variable");

    expect(
      formatInOutPrice({
        input_price_credits: 0,
        output_price_credits: 178_920_000,
        pricing_mode: "fixed",
      }),
    ).toBe("$0 / $0.179");
  });

  it("carries the unit through to both halves", () => {
    expect(
      formatInOutPrice(
        {
          input_price_credits: 89_460_000,
          output_price_credits: 178_920_000,
          pricing_mode: "fixed",
        },
        "credits",
      ),
    ).toBe("89,460,000 / 178,920,000");
  });
});
