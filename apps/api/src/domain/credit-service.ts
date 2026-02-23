import type { CreditBalance } from "./types";

const BDT_TO_CREDITS = 100;

export class CreditService {
  private readonly balances = new Map<string, CreditBalance>();

  constructor() {
    this.balances.set("user-1", {
      userId: "user-1",
      availableCredits: 5_000,
      purchasedCredits: 5_000,
      promoCredits: 0,
    });
  }

  getBalance(userId: string): CreditBalance {
    if (!this.balances.has(userId)) {
      this.balances.set(userId, {
        userId,
        availableCredits: 0,
        purchasedCredits: 0,
        promoCredits: 0,
      });
    }
    return this.balances.get(userId)!;
  }

  consume(userId: string, credits: number): boolean {
    const balance = this.getBalance(userId);
    if (balance.availableCredits < credits) {
      return false;
    }
    balance.availableCredits -= credits;
    if (balance.purchasedCredits > 0) {
      balance.purchasedCredits = Math.max(0, balance.purchasedCredits - credits);
    }
    return true;
  }

  topUp(userId: string, bdtAmount: number): CreditBalance {
    const credits = Math.floor(Math.max(0, bdtAmount) * BDT_TO_CREDITS);
    const balance = this.getBalance(userId);
    balance.availableCredits += credits;
    balance.purchasedCredits += credits;
    return balance;
  }
}
