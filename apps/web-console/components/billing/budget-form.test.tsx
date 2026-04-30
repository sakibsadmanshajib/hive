import { describe, it, expect } from "vitest";
import { subunitsToTaka, takaToSubunits } from "./budget-form";

describe("BudgetForm BDT subunit conversions", () => {
  it("subunitsToTaka renders integer + 2dp fraction", () => {
    expect(subunitsToTaka(0)).toBe("0.00");
    expect(subunitsToTaka(100)).toBe("1.00");
    expect(subunitsToTaka(150)).toBe("1.50");
    expect(subunitsToTaka(199_99)).toBe("199.99");
    expect(subunitsToTaka(100_000_00)).toBe("100000.00");
  });

  it("subunitsToTaka guards against negative + non-finite input", () => {
    expect(subunitsToTaka(-1)).toBe("0.00");
    expect(subunitsToTaka(Number.NaN)).toBe("0.00");
    expect(subunitsToTaka(Number.POSITIVE_INFINITY)).toBe("0.00");
  });

  it("takaToSubunits accepts integer + 2dp decimal forms", () => {
    expect(takaToSubunits("0")).toBe(0);
    expect(takaToSubunits("1")).toBe(100);
    expect(takaToSubunits("1.5")).toBe(150);
    expect(takaToSubunits("1.50")).toBe(150);
    expect(takaToSubunits("1000")).toBe(100_000);
    expect(takaToSubunits("1000.99")).toBe(100_099);
  });

  it("takaToSubunits rejects malformed input", () => {
    expect(takaToSubunits("")).toBe(null);
    expect(takaToSubunits("   ")).toBe(null);
    expect(takaToSubunits("-1")).toBe(null);
    expect(takaToSubunits("1.123")).toBe(null);
    expect(takaToSubunits("abc")).toBe(null);
    expect(takaToSubunits("$1000")).toBe(null);
  });

  it("round-trips: takaToSubunits ∘ subunitsToTaka is identity for valid amounts", () => {
    const samples = [0, 100, 150, 9999, 100_000_00, 250_50];
    for (const s of samples) {
      const taka = subunitsToTaka(s);
      expect(takaToSubunits(taka)).toBe(s);
    }
  });

  it("BDT-only — no USD or FX strings in the conversion output", () => {
    const result = subunitsToTaka(150_000_00);
    expect(result).not.toContain("$");
    expect(result).not.toContain("USD");
    expect(result).not.toContain("exchange");
    expect(result).not.toContain("rate");
  });
});
