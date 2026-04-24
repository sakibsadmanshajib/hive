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
