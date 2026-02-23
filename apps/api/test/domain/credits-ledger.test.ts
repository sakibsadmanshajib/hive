import { describe, expect, it } from "vitest";

import { CreditLedger } from "../../src/domain/credits-ledger";

describe("CreditLedger", () => {
  it("reserves then settles using actual credits", () => {
    const ledger = new CreditLedger();
    const userId = "user-1";

    ledger.mintPurchased(userId, 1000, "pay-1");
    const reservationId = ledger.reserve(userId, "req-1", 250);
    ledger.settle(reservationId, 180);

    const balance = ledger.balance(userId);
    expect(balance.total).toBe(820);
    expect(balance.reserved).toBe(0);
    expect(balance.available).toBe(820);
  });

  it("fails reservation when available credits are insufficient", () => {
    const ledger = new CreditLedger();
    const userId = "user-1";

    ledger.mintPurchased(userId, 1000, "pay-1");

    expect(() => ledger.reserve(userId, "req-2", 4000)).toThrowError(
      "insufficient credits",
    );
  });

  it("consumes purchased first then promo credits", () => {
    const ledger = new CreditLedger();
    const userId = "user-1";

    ledger.mintPurchased(userId, 100, "pay-1");
    ledger.mintPromo(userId, 50, "eid");
    ledger.consume(userId, "req-1", 120);

    const balance = ledger.balance(userId);
    expect(balance.total).toBe(30);
    expect(balance.available).toBe(30);
  });
});
