import { describe, expect, it } from "vitest";

import { CreditLedger } from "../../src/domain/credits-ledger";
import { PaymentService } from "../../src/domain/payment-service";

describe("PaymentService", () => {
  it("does not duplicate credits for duplicate provider callback", () => {
    const ledger = new CreditLedger();
    const service = new PaymentService(ledger);

    service.createIntent("intent-1", "user-1", 100);
    service.handleVerifiedEvent("bkash", "bkash-abc", "intent-1", true);
    service.handleVerifiedEvent("bkash", "bkash-abc", "intent-1", true);

    expect(ledger.balance("user-1").total).toBe(10000);
  });

  it("never mints credits for unverified event", () => {
    const ledger = new CreditLedger();
    const service = new PaymentService(ledger);

    service.createIntent("intent-2", "user-2", 100);
    service.handleVerifiedEvent("sslcommerz", "ssl-abc", "intent-2", false);

    expect(ledger.balance("user-2").total).toBe(0);
  });
});
