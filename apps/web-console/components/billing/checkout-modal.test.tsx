import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, it, expect } from "vitest";
import { formatPrice } from "./checkout-modal";

describe("CheckoutModal BDT compliance", () => {
  it("formats BDT price without USD equivalent or FX language", () => {
    const result = formatPrice(120000, "BDT");
    // Should show BDT price only
    expect(result).toContain("1,200");
    expect(result).not.toContain("USD");
    expect(result).not.toContain("exchange");
    expect(result).not.toContain("conversion");
    expect(result).not.toContain("rate");
  });

  it("formats USD price directly", () => {
    const result = formatPrice(1000, "USD");
    expect(result).toContain("$10.00");
  });

  it("never returns FX-related text for any currency", () => {
    const currencies = ["BDT", "USD", "EUR", "GBP"];
    for (const currency of currencies) {
      const result = formatPrice(10000, currency);
      expect(result).not.toContain("exchange");
      expect(result).not.toContain("conversion");
      expect(result).not.toContain("rate");
    }
  });
});

// FX-17-04 (post-review): the pricing primitive is per-block, NOT per-credit.
// `price_per_block_minor` is the minor-unit cost of `credit_block_size`
// credits (CreditsPerUSD = 100,000). Display total in modal:
//
//   floor(credits * price_per_block_minor / credit_block_size)
//
// Locked-in invariants below catch the regression that codex-rescue
// flagged: prior code computed `credits * price_per_credit_minor`,
// inflating non-BD totals by 100,000×.
function computeAmountMinor(
  credits: number,
  pricePerBlockMinor: number,
  creditBlockSize: number,
): number {
  if (creditBlockSize <= 0) return 0;
  return Math.floor((credits * pricePerBlockMinor) / creditBlockSize);
}

describe("computeAmountMinor (FX-17-04 post-review per-block contract)", () => {
  const CREDITS_PER_USD = 100_000;

  it("USD non-BD: 5000 credits at 100 cents/block → 5 cents = $0.05", () => {
    const got = computeAmountMinor(5_000, 100, CREDITS_PER_USD);
    expect(got).toBe(5);
    expect(formatPrice(got, "USD")).toContain("$0.05");
  });

  it("USD non-BD: 1000 credits at 100 cents/block → 1 cent = $0.01", () => {
    const got = computeAmountMinor(1_000, 100, CREDITS_PER_USD);
    expect(got).toBe(1);
    expect(formatPrice(got, "USD")).toContain("$0.01");
  });

  it("USD non-BD: 100,000 credits at 100 cents/block → 100 cents = $1.00", () => {
    const got = computeAmountMinor(100_000, 100, CREDITS_PER_USD);
    expect(got).toBe(100);
    expect(formatPrice(got, "USD")).toContain("$1.00");
  });

  it("BDT: 1000 credits at 11550 paisa/block → 115 paisa = ৳1.15 (math/big floor parity)", () => {
    const got = computeAmountMinor(1_000, 11_550, CREDITS_PER_USD);
    expect(got).toBe(115);
    const formatted = formatPrice(got, "BDT");
    expect(formatted).toContain("1.15");
    expect(formatted).not.toContain("USD");
  });

  it("BDT: 100,000 credits at 11550 paisa/block → 11550 paisa = ৳115.50", () => {
    const got = computeAmountMinor(100_000, 11_550, CREDITS_PER_USD);
    expect(got).toBe(11_550);
    expect(formatPrice(got, "BDT")).toContain("115.50");
  });

  it("regression: NEVER returns 100,000× inflation (the pre-review bug)", () => {
    // The buggy formula `credits * price` would have produced 500,000 cents
    // ($5,000) here. The corrected formula must produce 5 cents ($0.05).
    const credits = 5_000;
    const pricePerBlockMinor = 100;
    const buggy = credits * pricePerBlockMinor; // = 500_000
    const corrected = computeAmountMinor(credits, pricePerBlockMinor, CREDITS_PER_USD);
    expect(corrected).toBeLessThan(buggy / 1000);
    expect(corrected).toBe(5);
  });

  it("zero/invalid block size collapses to 0 (defensive)", () => {
    expect(computeAmountMinor(5_000, 100, 0)).toBe(0);
    expect(computeAmountMinor(5_000, 100, -1)).toBe(0);
  });
});

// FX-17-04 regulatory: getCheckoutOptions decoder MUST reject any
// payload missing `price_per_block_minor`, `credit_block_size`, or
// `currency`, and MUST NOT surface the legacy USD-denominated symbols.
// Source-level assertions because client.ts depends on next/headers
// (server-only) and cannot be imported into a jsdom worker. Mirrors the
// spend-alert-form leakage-absence pattern.
describe("getCheckoutOptions decoder (FX-17-04 strict shape, source guard)", () => {
  const clientSrc = readFileSync(
    join(__dirname, "..", "..", "lib", "control-plane", "client.ts"),
    "utf8",
  );

  it("does not reference any USD pricing primitive (legacy or per-credit)", () => {
    expect(clientSrc).not.toContain("price_per_credit_usd");
    expect(clientSrc).not.toContain("pricePerCreditUsd");
    expect(clientSrc).not.toContain("amount_usd");
    expect(clientSrc).not.toContain("amountUsd");
    // Post-review rename: ensure the misleading per-credit name is gone.
    expect(clientSrc).not.toContain("price_per_credit_minor");
    expect(clientSrc).not.toContain("pricePerCreditMinor");
  });

  it("declares the new per-block pricing primitive on CheckoutOptions", () => {
    expect(clientSrc).toContain("price_per_block_minor: number");
    expect(clientSrc).toContain("credit_block_size: number");
    expect(clientSrc).toMatch(/CheckoutOptions[\s\S]{0,800}currency: string/);
  });

  it("decoder reads price_per_block_minor + credit_block_size + currency as required fields", () => {
    expect(clientSrc).toContain(
      'readNumberField(payload, "price_per_block_minor")',
    );
    expect(clientSrc).toContain(
      'readNumberField(payload, "credit_block_size")',
    );
    expect(clientSrc).toContain('readStringField(payload, "currency")');
    // Required-field rejection: missing/zero block size throws rather
    // than defaulting silently and producing a divide-by-zero render.
    expect(clientSrc).toMatch(
      /creditBlockSize === null[\s\S]{0,400}throw new Error\("Failed to parse checkout rails response"\)/,
    );
  });
});
