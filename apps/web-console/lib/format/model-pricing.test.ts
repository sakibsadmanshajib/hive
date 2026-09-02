import { describe, expect, it } from "vitest";

import {
  formatCachePrice,
  formatInOutPrice,
  formatModelPrice,
} from "./model-pricing";
import { CURRENCY_MARK } from "@/tests/support/currency-mark";

// This is a billing surface. The whole point of these branches is that
// "deliberately free", "variable by design", "no such rate" and "we could not
// read the price" are different answers, and collapsing any pair of them
// misleads a customer pricing the gateway.
//
// The unit is Hive credits per million metered tokens, and nothing else. This
// module rendered US dollars until issue #1694, and the model detail page
// printed the dollar figure and the credit integer for the same rate side by
// side, which is a conversion table: two renderings of one quantity publish
// the credit peg (owner ruling, .wolf/decisions.md D-070).

describe("formatModelPrice", () => {
  it("prints a published rate as credits per million tokens", () => {
    // The live deepseek-v4-flash input and output rates.
    expect(formatModelPrice(89_460_000, "fixed")).toBe("89,460,000");
    expect(formatModelPrice(178_920_000, "fixed")).toBe("178,920,000");
  });

  it("prints a published zero as zero, never as an absence", () => {
    // hive-default publishes cache_write_price_credits = 0
    // (supabase/migrations/20260824_02_free_pool_router.sql). Zero is a
    // decision: that dimension is not charged. Rendering it as "Unknown" or
    // "Free" would hide a real published rate behind a word.
    expect(formatModelPrice(0, "fixed")).toBe("0");
    expect(formatModelPrice(0, "upstream_actual")).toBe("0");
  });

  it("calls a missing price on a variable alias Variable", () => {
    expect(formatModelPrice(null, "upstream_actual")).toBe("Variable");
  });

  it("calls a missing price on a fixed alias Unknown, not Variable", () => {
    // A fixed alias with no price means the lookup failed. Saying "Variable"
    // there would present a broken decode as a pricing model.
    expect(formatModelPrice(null, "fixed")).toBe("Unknown");
    expect(formatModelPrice(null, "")).toBe("Unknown");
  });

  it("never renders a rate through a currency formatter", () => {
    // Every distinct non-zero rate the seeded catalog actually holds, read off
    // a database with the full migration chain applied, plus one credit, the
    // smallest amount the unit can express at all.
    //
    // The old dollar renderer needed a per-value precision rule here, because
    // two decimals printed a real 2,982,000 credit cache-read rate as "$0.00"
    // and a price that looks free is worse than no price. An integer count
    // cannot round to zero, so what is left to pin is that no currency comes
    // back and that a real rate never reads as the published zero above.
    const everyRealRate = [
      1, 10_000, 40_000, 1_000_000, 2_982_000, 4_000_000, 52_360_000,
      89_460_000, 105_000_000, 178_920_000, 210_000_000, 420_000_000,
      840_000_000, 1_570_800_000, 4_712_400_000, 30_800_000_000,
      43_166_670_000,
    ];
    for (const credits of everyRealRate) {
      const rendered = formatModelPrice(credits, "fixed");
      expect(rendered, `${credits} credits rendered with a currency`).not.toMatch(
        CURRENCY_MARK,
      );
      expect(rendered, `${credits} credits rendered as free`).not.toBe("0");
    }
    expect(formatModelPrice(1, "fixed")).toBe("1");
  });
});

describe("formatCachePrice", () => {
  it("distinguishes no-cache-rate from a broken lookup and from free", () => {
    expect(formatCachePrice(null, "fixed")).toBe("—");
    expect(formatCachePrice(null, "upstream_actual")).toBe("Variable");
    expect(formatCachePrice(0, "fixed")).toBe("0");
  });

  it("renders a real cache rate rather than a dash", () => {
    // The corrected deepseek-v4-flash cache-read rate, 1/30 of its input rate
    // (supabase/migrations/20260825_02_deepseek_cache_read_price_correction.sql).
    expect(formatCachePrice(2_982_000, "fixed")).toBe("2,982,000");
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
    ).toBe("89,460,000 / 178,920,000");
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
    ).toBe("0 / 178,920,000");
  });
});
