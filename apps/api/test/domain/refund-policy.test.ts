import { describe, expect, it } from "vitest";

import { CreditLedger } from "../../src/domain/credits-ledger";
import { RefundPolicy } from "../../src/domain/refund-policy";

describe("RefundPolicy", () => {
  it("uses 30-day window and 90 percent rate", () => {
    const now = new Date("2026-02-23T00:00:00.000Z");
    const ledger = new CreditLedger(() => now);
    const userId = "user-1";

    ledger.mintPurchased(userId, 1000, "recent-pay", new Date("2026-02-10T00:00:00.000Z"));
    ledger.mintPurchased(userId, 1000, "old-pay", new Date("2025-12-01T00:00:00.000Z"));
    ledger.consume(userId, "req-1", 400);
    ledger.mintPromo(userId, 500, "eid");

    const policy = new RefundPolicy(ledger);
    const quote = policy.quote(userId);

    expect(quote.refundableCredits).toBe(600);
    expect(quote.refundBdt).toBe(5.4);
  });
});
