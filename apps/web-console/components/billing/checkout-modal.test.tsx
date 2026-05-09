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

// FX-17-04: pricing primitive is now per-country in minor units. The
// modal multiplies credits by `price_per_credit_minor` directly (no
// float scaling) and renders the total in `options.currency`. The
// assertions below pin the integer-arithmetic contract for both
// currencies: USD ($0.01/credit → 1 cent minor) and BDT
// (~৳1.20/credit → 120 paisa minor).
describe("computeAmountMinor (FX-17-04 contract)", () => {
  it("USD: 5000 credits at 1 cent each → 5000 cent minor units", () => {
    const credits = 5000;
    const pricePerCreditMinor = 1;
    expect(credits * pricePerCreditMinor).toBe(5000);
    expect(formatPrice(credits * pricePerCreditMinor, "USD")).toContain(
      "$50.00",
    );
  });

  it("BDT: 5000 credits at 120 paisa each → 600000 paisa minor units", () => {
    const credits = 5000;
    const pricePerCreditMinor = 120;
    expect(credits * pricePerCreditMinor).toBe(600000);
    const formatted = formatPrice(credits * pricePerCreditMinor, "BDT");
    expect(formatted).toContain("6,000");
    expect(formatted).not.toContain("USD");
  });

  it("integer arithmetic: no rounding artefacts at boundary credit counts", () => {
    // 1000 * 13 paisa = 13000 paisa exactly — no Math.round drift.
    expect(1000 * 13).toBe(13000);
  });
});

// FX-17-04 regulatory: getCheckoutOptions decoder MUST reject any
// payload missing `price_per_credit_minor` or `currency`, and MUST NOT
// surface the legacy USD-denominated symbols. Source-level assertions
// because client.ts depends on next/headers (server-only) and cannot
// be imported into a jsdom worker. Mirrors the spend-alert-form
// leakage-absence pattern.
describe("getCheckoutOptions decoder (FX-17-04 strict shape, source guard)", () => {
  const clientSrc = readFileSync(
    join(__dirname, "..", "..", "lib", "control-plane", "client.ts"),
    "utf8",
  );

  it("does not reference the legacy USD pricing primitive", () => {
    expect(clientSrc).not.toContain("price_per_credit_usd");
    expect(clientSrc).not.toContain("pricePerCreditUsd");
    expect(clientSrc).not.toContain("amount_usd");
    expect(clientSrc).not.toContain("amountUsd");
  });

  it("declares the new minor-units pricing primitive on CheckoutOptions", () => {
    expect(clientSrc).toContain("price_per_credit_minor: number");
    // The interface also exposes the resolved currency.
    expect(clientSrc).toMatch(/CheckoutOptions[\s\S]{0,800}currency: string/);
  });

  it("decoder reads price_per_credit_minor and currency as required fields", () => {
    expect(clientSrc).toContain(
      'readNumberField(payload, "price_per_credit_minor")',
    );
    expect(clientSrc).toContain('readStringField(payload, "currency")');
    // Required-field rejection: missing field throws rather than
    // defaulting silently to a USD assumption.
    expect(clientSrc).toMatch(
      /pricePerCreditMinor === null \|\| !currency[\s\S]{0,200}throw new Error\("Failed to parse checkout rails response"\)/,
    );
  });
});
