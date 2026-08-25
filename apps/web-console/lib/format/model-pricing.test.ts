import { describe, expect, it } from "vitest";

import { formatInOutPrice, formatModelPrice } from "./model-pricing";

// This is a billing surface. The whole point of these three branches is that
// "deliberately free", "variable by design" and "we could not read the price"
// are different answers, and collapsing any pair of them misleads a customer
// pricing the gateway. Each case below is one of those collapses.
describe("formatModelPrice", () => {
  it("prints a published rate, thousand-separated", () => {
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
