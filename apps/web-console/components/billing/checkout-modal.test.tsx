import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, it, expect } from "vitest";
import { formatCurrency as formatPrice } from "@/lib/format/money";
import { computeAmountMinor } from "./checkout-modal";

describe("CheckoutModal price formatting", () => {
  it("formats BDT price", () => {
    const result = formatPrice(120000, "BDT");
    expect(result).toContain("1,200");
  });

  it("formats USD price directly", () => {
    const result = formatPrice(1000, "USD");
    expect(result).toContain("$10.00");
  });
});

// The selected rail publishes the exact price of one credit in its own minor
// unit, as a fraction. The modal renders
//
//   floor(credits * price_minor_numerator / price_credits_denominator)
//
// and that has to equal what the control plane charges for the same quantity,
// because it is the same fraction truncated the same way (issue #1737).
//
// Every case calls the PRODUCTION helper, not a copy of its arithmetic: a copy
// would keep passing after the component's real computation regressed, which is
// what the FX-17-04 review pass found the first time round.
describe("computeAmountMinor", () => {
  const CREDITS_PER_USD = 1_000_000_000;
  // 1.06 US cents per USD block: the peg (1 USD = 1e9 credits, D-046) plus the
  // 6 percent purchase markup (D-065), reduced. This is verbatim what the
  // control plane publishes for a Stripe rail.
  const USD = { num: 53, den: 500_000_000 };

  it("USD: 50M credits is 5 cents", () => {
    const got = computeAmountMinor(50_000_000, USD.num, USD.den);
    expect(got).toBe(5);
    expect(formatPrice(got, "USD")).toContain("$0.05");
  });

  it("USD: one block is 106 cents, the peg plus the 6 percent markup", () => {
    const got = computeAmountMinor(CREDITS_PER_USD, USD.num, USD.den);
    expect(got).toBe(106);
    expect(formatPrice(got, "USD")).toContain("$1.06");
  });

  it("USD: twenty blocks is 21.20, twenty times the single-block price", () => {
    const got = computeAmountMinor(20 * CREDITS_PER_USD, USD.num, USD.den);
    expect(got).toBe(2120);
    expect(formatPrice(got, "USD")).toContain("$21.20");
  });

  // The D-066 worked example: a mid rate of 127 carries a flat 2.5 percent
  // markup folded into the rate, giving an effective 130.175. One credit then
  // costs 1.06 * 130.175 / 1e7 paisa, which is 275971 / 20000000000.
  const BDT = { num: 275_971, den: 20_000_000_000 };

  it("BDT: one block at a mid rate of 127 is 138.00 taka", () => {
    const got = computeAmountMinor(CREDITS_PER_USD, BDT.num, BDT.den);
    expect(got).toBe(13_798);
    expect(formatPrice(got, "BDT")).toContain("137.98");
  });

  it("BDT: twenty blocks keeps the paisa a per-block price would have dropped", () => {
    // The exact price is 275971 paisa for twenty blocks. The predecessor sent
    // the block price already truncated to 13798 paisa and multiplied, landing
    // on 275960 and under-quoting the payer by 11 paisa. That gap is the reason
    // the fraction is on the wire.
    const got = computeAmountMinor(20 * CREDITS_PER_USD, BDT.num, BDT.den);
    expect(got).toBe(275_971);
    expect(got - 20 * 13_798).toBe(11);
  });

  it("stays exact past the point a Number product would round", () => {
    // 5e12 credits against this numerator is ~1.4e18 before the division, well
    // past 2^53 - 1. BigInt carries it; a Number product would not.
    const credits = 5_000_000_000_000;
    const reference = Number(
      (BigInt(credits) * BigInt(BDT.num)) / BigInt(BDT.den),
    );
    expect(Number.isSafeInteger(reference)).toBe(true);
    expect(computeAmountMinor(credits, BDT.num, BDT.den)).toBe(reference);
  });

  it("floors rather than rounding, matching the control plane's truncation", () => {
    // One credit short of a whole paisa must not round up into one.
    expect(computeAmountMinor(3, 1, 2)).toBe(1);
    expect(computeAmountMinor(1, 1, 2)).toBe(0);
  });

  it("renders no amount rather than throwing on an unusable price", () => {
    // BigInt() throws on a non-integer, on NaN and on Infinity, and a throw
    // inside render would blank the whole modal.
    expect(computeAmountMinor(50_000_000, 53, 0)).toBe(0);
    expect(computeAmountMinor(50_000_000, 53, -1)).toBe(0);
    expect(computeAmountMinor(50_000_000, Number.NaN, 500_000_000)).toBe(0);
    expect(computeAmountMinor(50_000_000, 53, Number.POSITIVE_INFINITY)).toBe(0);
    expect(computeAmountMinor(50_000_000.5, 53, 500_000_000)).toBe(0);
    expect(computeAmountMinor(-1, 53, 500_000_000)).toBe(0);
  });
});

// Source-level assertions because client.ts depends on next/headers
// (server-only) and cannot be imported into a jsdom worker.
describe("getCheckoutRails decoder (source guard)", () => {
  const clientSrc = readFileSync(
    join(__dirname, "..", "..", "lib", "control-plane", "client.ts"),
    "utf8",
  );

  it("does not reference either superseded pricing name", () => {
    // price_per_credit_minor inflated a non-BD total 100,000 times over.
    // price_per_block_minor replaced it and then under-quoted a taka payer,
    // because a block price truncated to whole minor units cannot carry a
    // three decimal rate. Neither name may come back.
    expect(clientSrc).not.toContain("price_per_credit_minor");
    expect(clientSrc).not.toContain("price_per_block_minor");
    expect(clientSrc).not.toContain("credit_block_size");
  });

  it("declares the price on the rail, not on the account", () => {
    expect(clientSrc).toMatch(
      /CheckoutRail[\s\S]{0,1400}price_minor_numerator: number;[\s\S]{0,200}price_credits_denominator: number;/,
    );
  });

  it("drops a rail whose price is absent or unusable", () => {
    expect(clientSrc).toContain(
      'readNumberField(value, "price_minor_numerator")',
    );
    expect(clientSrc).toContain(
      'readNumberField(value, "price_credits_denominator")',
    );
    // A rail that cannot be priced is dropped, never defaulted: an invented
    // money figure is how issue #1386 displayed a purchase ceiling 100 times
    // too low with nothing complaining.
    expect(clientSrc).toMatch(
      /numerator === null[\s\S]{0,400}denominator <= 0\s*\)\s*\{\s*return null;/,
    );
  });
});
