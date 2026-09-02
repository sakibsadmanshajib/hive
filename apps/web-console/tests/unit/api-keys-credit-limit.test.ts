/**
 * The credit-limit input behind the New Key modal.
 *
 * This is a money path, not a formatting helper: whatever integer comes out of
 * here is written to `api_key_policies.budget_limit_credits` and is the exact
 * number `apps/edge-api/internal/authz.CheckAccess` compares every subsequent
 * request against. A value that is off by one credit is a wrong enforcement
 * ceiling, and a value that silently becomes null is an uncapped key on an
 * account that believes it is capped.
 *
 * The field took US dollars and multiplied by 1e9 until issue #1694. It takes
 * credits now, because the cap renders in the keys table beside a
 * credit-denominated spend figure and its own bar, and a dollar cap next to a
 * credit figure for the same key publishes the credit peg (owner ruling,
 * .wolf/decisions.md D-070). The stored unit did not change.
 *
 * The invariant pinned here: for a customer-typed credit count C,
 * parseCreditLimitInput returns exactly C as an integer, or null. It never
 * returns a number that is not exactly the amount typed, and it never returns
 * zero or a negative, because the budget check treats those as caps that
 * refuse every request rather than as an absent cap.
 */
import { describe, expect, it } from "vitest";

import { parseCreditLimitInput } from "@/lib/api-keys";
import { CREDITS_PER_USD } from "@/lib/format/model-pricing";

describe("parseCreditLimitInput", () => {
  it("takes a plain credit count exactly", () => {
    expect(parseCreditLimitInput("1")).toBe(1);
    expect(parseCreditLimitInput("10000000000")).toBe(10_000_000_000);
    expect(parseCreditLimitInput("250")).toBe(250);
  });

  it("accepts grouping separators, because a real cap is a twelve-digit number", () => {
    expect(parseCreditLimitInput("10,000,000,000")).toBe(10_000_000_000);
    expect(parseCreditLimitInput("1,000")).toBe(1_000);
    // Exactly the credit unit, which is the figure a customer converting an
    // old dollar cap by hand is most likely to type.
    expect(parseCreditLimitInput("1,000,000,000")).toBe(CREDITS_PER_USD);
  });

  it("refuses separators in positions that are not the canonical grouping", () => {
    // "1,0,0" read as 100 would set a cap two orders of magnitude below the
    // one the customer meant to type.
    expect(parseCreditLimitInput("1,0,0")).toBeNull();
    expect(parseCreditLimitInput("1,00,000")).toBeNull();
    expect(parseCreditLimitInput(",100")).toBeNull();
    expect(parseCreditLimitInput("100,")).toBeNull();
  });

  it("trims surrounding whitespace", () => {
    expect(parseCreditLimitInput("  5,000  ")).toBe(5_000);
  });

  it("refuses a fractional credit, since the ledger has no such quantity", () => {
    // Truncating "10.5" to 10 would set a cap the customer did not type, and
    // rounding it up would set one they did not agree to either.
    expect(parseCreditLimitInput("10.5")).toBeNull();
    expect(parseCreditLimitInput("0.000000001")).toBeNull();
  });

  it("treats a blank field as no cap rather than a zero cap", () => {
    expect(parseCreditLimitInput("")).toBeNull();
    expect(parseCreditLimitInput("   ")).toBeNull();
  });

  it("refuses zero and negatives, which would refuse every request", () => {
    // "0 (unlimited)" is upstream terminology, never a real cap here: the
    // budget check refuses when consumed + reserved + estimated exceeds the
    // limit, and every request carries a positive estimate.
    expect(parseCreditLimitInput("0")).toBeNull();
    expect(parseCreditLimitInput("-5")).toBeNull();
  });

  it("refuses anything that is not a plain credit count", () => {
    expect(parseCreditLimitInput("1e9")).toBeNull();
    expect(parseCreditLimitInput("ten")).toBeNull();
    expect(parseCreditLimitInput("10 credits")).toBeNull();
    expect(parseCreditLimitInput("$10")).toBeNull();
    expect(parseCreditLimitInput("Infinity")).toBeNull();
    expect(parseCreditLimitInput("NaN")).toBeNull();
  });

  it("refuses an amount that cannot survive as a JS-safe integer", () => {
    // Past Number.MAX_SAFE_INTEGER the value on the wire is already a
    // different number from the one typed, so the honest answer is a refusal
    // rather than a rounded cap the customer never chose. The proxy route
    // rejects the same range independently.
    expect(parseCreditLimitInput("9007199254740991")).toBe(
      Number.MAX_SAFE_INTEGER,
    );
    expect(parseCreditLimitInput("9007199254740992")).toBeNull();
  });
});
