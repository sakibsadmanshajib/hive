import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, it, expect } from "vitest";
import { formatCurrency as formatPrice } from "@/lib/format/money";
import { computeBlockSplitAmountMinor } from "./checkout-modal";

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

// FX-17-04 (post-review): the pricing primitive is per-block, NOT per-credit.
// `price_per_block_minor` is the minor-unit cost of `credit_block_size`
// credits (CreditsPerUSD = 1,000,000,000 since the 2026-08-23 credit unit
// rescale). Display total in modal:
//
//   floor(credits * price_per_block_minor / credit_block_size)
//
// Locked-in invariants below catch the regression that codex-rescue
// flagged: prior code computed `credits * price_per_credit_minor`,
// inflating non-BD totals by 100,000×.
// The test cases call the PRODUCTION helper (argument order adapted), not a
// copy of its math: a copy would keep passing after the component's real
// computation regressed.
function computeAmountMinor(
  credits: number,
  pricePerBlockMinor: number,
  creditBlockSize: number,
): number {
  return computeBlockSplitAmountMinor(credits, creditBlockSize, pricePerBlockMinor);
}

describe("computeAmountMinor (FX-17-04 post-review per-block contract)", () => {
  const CREDITS_PER_USD = 1_000_000_000;

  it("USD non-BD: 50M credits at 100 cents/block → 5 cents = $0.05", () => {
    const got = computeAmountMinor(50_000_000, 100, CREDITS_PER_USD);
    expect(got).toBe(5);
    expect(formatPrice(got, "USD")).toContain("$0.05");
  });

  it("USD non-BD: 10M credits at 100 cents/block → 1 cent = $0.01", () => {
    const got = computeAmountMinor(10_000_000, 100, CREDITS_PER_USD);
    expect(got).toBe(1);
    expect(formatPrice(got, "USD")).toContain("$0.01");
  });

  it("USD non-BD: 1B credits at 100 cents/block → 100 cents = $1.00", () => {
    const got = computeAmountMinor(1_000_000_000, 100, CREDITS_PER_USD);
    expect(got).toBe(100);
    expect(formatPrice(got, "USD")).toContain("$1.00");
  });

  it("BDT: 10M credits at 11550 paisa/block → 115 paisa = ৳1.15 (floor parity)", () => {
    const got = computeAmountMinor(10_000_000, 11_550, CREDITS_PER_USD);
    expect(got).toBe(115);
    const formatted = formatPrice(got, "BDT");
    expect(formatted).toContain("1.15");
  });

  it("BDT: 1B credits at 11550 paisa/block → 11550 paisa = ৳115.50", () => {
    const got = computeAmountMinor(1_000_000_000, 11_550, CREDITS_PER_USD);
    expect(got).toBe(11_550);
    expect(formatPrice(got, "BDT")).toContain("115.50");
  });

  it("magnitude: max-size purchase stays exact past 2^53 raw product", () => {
    // 5e12 credits x 15000 paisa would be 7.5e16 if multiplied naively,
    // which is past Number.MAX_SAFE_INTEGER. The block-split computation
    // must still return the exact floor.
    const got = computeAmountMinor(5_000_000_000_000, 15_000, CREDITS_PER_USD);
    expect(got).toBe(75_000_000); // BDT 500K in paisa
  });

  it("magnitude: non-aligned input matches a BigInt reference exactly", () => {
    const credits = 5_000_775_014_999;
    const price = 15_001;
    const blockSize = CREDITS_PER_USD;
    const reference =
      Number((BigInt(credits) * BigInt(price)) / BigInt(blockSize));
    expect(Number.isSafeInteger(reference)).toBe(true);
    expect(computeBlockSplitAmountMinor(credits, blockSize, price)).toBe(reference);
  });

  it("regression: NEVER returns per-credit inflation (the pre-review bug)", () => {
    // The buggy formula `credits * price` would have produced 500,000,000
    // cents here. The corrected formula must produce 5 cents ($0.05).
    const credits = 50_000_000;
    const pricePerBlockMinor = 100;
    const buggy = credits * pricePerBlockMinor;
    const corrected = computeAmountMinor(credits, pricePerBlockMinor, CREDITS_PER_USD);
    expect(corrected).toBeLessThan(buggy / 1000);
    expect(corrected).toBe(5);
  });

  it("zero/invalid block size collapses to 0 (defensive)", () => {
    expect(computeAmountMinor(50_000_000, 100, 0)).toBe(0);
    expect(computeAmountMinor(50_000_000, 100, -1)).toBe(0);
  });
});

// FX-17-04: getCheckoutOptions decoder MUST reject any payload missing
// `price_per_block_minor`, `credit_block_size`, or `currency`. Source-level
// assertions because client.ts depends on next/headers (server-only) and
// cannot be imported into a jsdom worker.
describe("getCheckoutOptions decoder (FX-17-04 strict shape, source guard)", () => {
  const clientSrc = readFileSync(
    join(__dirname, "..", "..", "lib", "control-plane", "client.ts"),
    "utf8",
  );

  it("does not reference the misleading per-credit pricing name (post-review rename)", () => {
    // Post-review rename: ensure the misleading per-credit name (which
    // caused the 100,000x non-BD total inflation bug) is gone in favor of
    // the per-block primitive.
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
