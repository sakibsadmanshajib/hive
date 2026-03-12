import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { PaymentReconciliationScheduler } from "../../src/runtime/payment-reconciliation-scheduler";

describe("PaymentReconciliationScheduler", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("runs reconciliation on the configured interval", async () => {
    const reconcileRecentPayments = vi.fn().mockResolvedValue({
      summary: {
        totalFindings: 1,
        verifiedEventWithoutCreditedIntent: 1,
        creditedIntentWithoutVerifiedEvent: 0,
        creditedAmountMismatch: 0,
      },
      findings: [{ kind: "verified_event_without_credited_intent", intentId: "intent_1" }],
    });
    const logger = { warn: vi.fn(), error: vi.fn() };
    const scheduler = new PaymentReconciliationScheduler(
      { reconcileRecentPayments },
      logger,
      { intervalMs: 60_000, lookbackHours: 6 },
    );

    scheduler.start();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(60_000);

    expect(reconcileRecentPayments).toHaveBeenCalledTimes(2);
    expect(reconcileRecentPayments).toHaveBeenCalledWith(6);
    expect(logger.warn).toHaveBeenCalledTimes(2);
  });

  it("skips overlapping runs while a prior reconciliation is still in flight", async () => {
    let resolveRun: (() => void) | undefined;
    const reconcileRecentPayments = vi.fn().mockImplementation(
      () => new Promise<void>((resolve) => {
        resolveRun = resolve;
      }),
    );
    const logger = { warn: vi.fn(), error: vi.fn() };
    const scheduler = new PaymentReconciliationScheduler(
      { reconcileRecentPayments },
      logger,
      { intervalMs: 60_000, lookbackHours: 6 },
    );

    scheduler.start();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(60_000);
    await vi.advanceTimersByTimeAsync(60_000);

    expect(reconcileRecentPayments).toHaveBeenCalledTimes(1);

    resolveRun?.();
    await Promise.resolve();
  });

  it("logs reconciliation failures", async () => {
    const reconcileRecentPayments = vi.fn().mockRejectedValue(new Error("boom"));
    const logger = { warn: vi.fn(), error: vi.fn() };
    const scheduler = new PaymentReconciliationScheduler(
      { reconcileRecentPayments },
      logger,
      { intervalMs: 60_000, lookbackHours: 6 },
    );

    scheduler.start();
    await Promise.resolve();

    expect(logger.error).toHaveBeenCalledTimes(1);
    expect(logger.warn).not.toHaveBeenCalled();
  });

  it("does not log on clean intervals with no findings", async () => {
    const reconcileRecentPayments = vi.fn().mockResolvedValue({
      summary: {
        totalFindings: 0,
        verifiedEventWithoutCreditedIntent: 0,
        creditedIntentWithoutVerifiedEvent: 0,
        creditedAmountMismatch: 0,
        missingPaymentLedgerEntry: 0,
      },
      findings: [],
    });
    const logger = { warn: vi.fn(), error: vi.fn() };
    const scheduler = new PaymentReconciliationScheduler(
      { reconcileRecentPayments },
      logger,
      { intervalMs: 60_000, lookbackHours: 6 },
    );

    scheduler.start();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(60_000);

    expect(logger.warn).not.toHaveBeenCalled();
    expect(logger.error).not.toHaveBeenCalled();
  });
});
