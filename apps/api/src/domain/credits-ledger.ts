import { randomUUID } from "node:crypto";

export interface Balance {
  total: number;
  reserved: number;
  available: number;
}

export interface Reservation {
  reservationId: string;
  userId: string;
  requestId: string;
  estimatedCredits: number;
  settled: boolean;
}

export interface PurchasedLot {
  paymentId: string;
  originalCredits: number;
  remainingCredits: number;
  createdAt: Date;
}

export interface UsageEvent {
  requestId: string;
  userId: string;
  credits: number;
  createdAt: string;
}

export class CreditLedger {
  private readonly nowFn: () => Date;
  private readonly purchased = new Map<string, PurchasedLot[]>();
  private readonly promo = new Map<string, number>();
  private readonly reservations = new Map<string, Reservation>();
  private readonly usage: UsageEvent[] = [];

  constructor(nowFn?: () => Date) {
    this.nowFn = nowFn ?? (() => new Date());
  }

  mintPurchased(userId: string, credits: number, paymentId: string, createdAt?: Date): void {
    if (credits <= 0) {
      throw new Error("credits must be positive");
    }

    const lot: PurchasedLot = {
      paymentId,
      originalCredits: credits,
      remainingCredits: credits,
      createdAt: createdAt ?? this.nowFn(),
    };

    const current = this.purchased.get(userId) ?? [];
    current.push(lot);
    this.purchased.set(userId, current);
  }

  mintPromo(userId: string, credits: number, campaignId: string): void {
    if (credits <= 0) {
      throw new Error("credits must be positive");
    }
    void campaignId;
    this.promo.set(userId, (this.promo.get(userId) ?? 0) + credits);
  }

  reserve(userId: string, requestId: string, estimatedCredits: number): string {
    if (estimatedCredits <= 0) {
      throw new Error("estimated credits must be positive");
    }
    const balance = this.balance(userId);
    if (balance.available < estimatedCredits) {
      throw new Error("insufficient credits");
    }

    const reservationId = `res_${randomUUID().replace(/-/g, "").slice(0, 12)}`;
    this.reservations.set(reservationId, {
      reservationId,
      userId,
      requestId,
      estimatedCredits,
      settled: false,
    });
    return reservationId;
  }

  settle(reservationId: string, actualCredits: number): void {
    if (actualCredits < 0) {
      throw new Error("actual credits cannot be negative");
    }
    const reservation = this.reservations.get(reservationId);
    if (reservation === undefined) {
      throw new Error("reservation not found");
    }
    if (reservation.settled) {
      return;
    }

    this.consume(reservation.userId, reservation.requestId, actualCredits);
    reservation.settled = true;
  }

  consume(userId: string, requestId: string, credits: number): void {
    if (credits <= 0) {
      return;
    }
    const balance = this.balance(userId);
    if (balance.available < credits) {
      throw new Error("insufficient credits");
    }

    let remaining = credits;
    const lots = this.purchased.get(userId) ?? [];
    for (const lot of lots) {
      if (remaining === 0) {
        break;
      }
      const take = Math.min(lot.remainingCredits, remaining);
      lot.remainingCredits -= take;
      remaining -= take;
    }

    if (remaining > 0) {
      const promo = this.promo.get(userId) ?? 0;
      const take = Math.min(promo, remaining);
      this.promo.set(userId, promo - take);
      remaining -= take;
    }

    if (remaining > 0) {
      throw new Error("insufficient credits");
    }

    this.usage.push({
      requestId,
      userId,
      credits,
      createdAt: this.nowFn().toISOString(),
    });
  }

  balance(userId: string): Balance {
    const purchased = (this.purchased.get(userId) ?? []).reduce((sum, lot) => sum + lot.remainingCredits, 0);
    const promo = this.promo.get(userId) ?? 0;
    const total = purchased + promo;
    const reserved = [...this.reservations.values()]
      .filter((reservation) => reservation.userId === userId && !reservation.settled)
      .reduce((sum, reservation) => sum + reservation.estimatedCredits, 0);

    return {
      total,
      reserved,
      available: total - reserved,
    };
  }

  refundablePurchasedCredits(userId: string, withinDays: number): number {
    const cutoffMs = this.nowFn().getTime() - withinDays * 24 * 60 * 60 * 1000;
    return (this.purchased.get(userId) ?? [])
      .filter((lot) => lot.createdAt.getTime() >= cutoffMs)
      .reduce((sum, lot) => sum + lot.remainingCredits, 0);
  }

  usageEvents(): UsageEvent[] {
    return [...this.usage];
  }
}
