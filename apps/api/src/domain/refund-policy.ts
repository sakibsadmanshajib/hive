import { CreditLedger } from "./credits-ledger";

export interface RefundQuote {
  refundableCredits: number;
  refundBdt: number;
}

export class RefundPolicy {
  private readonly ledger: CreditLedger;
  private readonly windowDays: number;

  constructor(ledger: CreditLedger, windowDays = 30) {
    this.ledger = ledger;
    this.windowDays = windowDays;
  }

  quote(userId: string): RefundQuote {
    const credits = this.ledger.refundablePurchasedCredits(userId, this.windowDays);
    const refundBdt = (credits / 100) * 0.9;
    return {
      refundableCredits: credits,
      refundBdt: Math.round(refundBdt * 10000) / 10000,
    };
  }
}
