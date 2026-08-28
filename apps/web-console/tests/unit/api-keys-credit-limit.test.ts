/**
 * The dollar-to-credits conversion behind the New Key modal's credit limit.
 *
 * This is a money path, not a formatting helper: whatever integer comes out of
 * here is written to `api_key_policies.budget_limit_credits` and is the exact
 * number `apps/edge-api/internal/authz.CheckAccess` compares every subsequent
 * request against. A value that is off by one credit is a wrong enforcement
 * ceiling, and a value that silently becomes null is an uncapped key on an
 * account that believes it is capped. Neither had a test before this file.
 *
 * The invariant pinned here: for a customer-typed amount D dollars,
 * usdToCreditsInput returns exactly D * 1e9 as an integer, or null. It never
 * returns a number that is not exactly the amount typed, and it never returns
 * zero or a negative, because the budget check treats those as caps that
 * refuse every request rather than as an absent cap.
 */
import { describe, expect, it } from "vitest";

import { usdToCreditsInput } from "@/lib/api-keys";
import { CREDITS_PER_USD } from "@/lib/format/model-pricing";

describe("usdToCreditsInput", () => {
  it("converts whole dollars exactly", () => {
    expect(usdToCreditsInput("1")).toBe(CREDITS_PER_USD);
    expect(usdToCreditsInput("10")).toBe(10 * CREDITS_PER_USD);
    expect(usdToCreditsInput("250")).toBe(250 * CREDITS_PER_USD);
  });

  it("converts the decimal literals float multiplication gets wrong", () => {
    // 0.1 * 1e9 and 12.34 * 1e9 are the two shapes that motivated the
    // string-shifting implementation. Asserted against the integer arithmetic
    // the customer means, never against the float expression under test.
    expect(usdToCreditsInput("0.1")).toBe(100_000_000);
    expect(usdToCreditsInput("12.34")).toBe(12_340_000_000);
    expect(usdToCreditsInput("0.07")).toBe(70_000_000);
    expect(usdToCreditsInput("29.97")).toBe(29_970_000_000);
  });

  it("keeps trailing-zero and leading-zero forms of the same amount identical", () => {
    expect(usdToCreditsInput("10.00")).toBe(usdToCreditsInput("10"));
    expect(usdToCreditsInput("0.50")).toBe(usdToCreditsInput("0.5"));
    expect(usdToCreditsInput("007")).toBe(7 * CREDITS_PER_USD);
  });

  it("accepts the full nine fractional digits, which is one credit", () => {
    expect(usdToCreditsInput("0.000000001")).toBe(1);
  });

  it("refuses a tenth fractional digit rather than truncating it", () => {
    // Truncating would silently enforce a different ceiling from the one
    // typed, which is the failure mode this whole helper exists to avoid.
    expect(usdToCreditsInput("0.0000000001")).toBeNull();
    expect(usdToCreditsInput("1.1234567891")).toBeNull();
  });

  it("treats blank as no cap", () => {
    expect(usdToCreditsInput("")).toBeNull();
    expect(usdToCreditsInput("   ")).toBeNull();
  });

  it("refuses zero, negatives and non-numeric input", () => {
    // Zero is refused deliberately: the budget check denies when
    // consumed + reserved + estimated > limit, so a zero cap bricks the key on
    // its first request. A customer typing 0 means "no limit" on every
    // upstream console that accepts it, and this form means blank for that.
    expect(usdToCreditsInput("0")).toBeNull();
    expect(usdToCreditsInput("0.00")).toBeNull();
    expect(usdToCreditsInput("-5")).toBeNull();
    expect(usdToCreditsInput("1e9")).toBeNull();
    expect(usdToCreditsInput("ten")).toBeNull();
    expect(usdToCreditsInput("10.00 USD")).toBeNull();
    expect(usdToCreditsInput("$10")).toBeNull();
    expect(usdToCreditsInput("1,000")).toBeNull();
    expect(usdToCreditsInput("Infinity")).toBeNull();
    expect(usdToCreditsInput("NaN")).toBeNull();
  });

  it("refuses an amount that cannot survive as a JS-safe integer", () => {
    // Past Number.MAX_SAFE_INTEGER the value on the wire is already a
    // different number from the one typed, so the honest answer is a refusal
    // rather than a rounded cap the customer never chose. The proxy route
    // rejects the same range independently.
    expect(usdToCreditsInput("9007199.254740991")).toBe(Number.MAX_SAFE_INTEGER);
    expect(usdToCreditsInput("9007199.254740992")).toBeNull();
    expect(usdToCreditsInput("100000000")).toBeNull();
  });
});
