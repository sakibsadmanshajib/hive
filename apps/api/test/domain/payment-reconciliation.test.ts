import { describe, expect, it } from "vitest";

import type { PaymentReconciliationSnapshot } from "../../src/domain/types";
import { PaymentReconciliationService, reconcilePaymentDrift } from "../../src/runtime/payment-reconciliation";

describe("reconcilePaymentDrift", () => {
  it("flags verified payment events whose intents were not credited", () => {
    const snapshot: PaymentReconciliationSnapshot = {
      intents: [
        {
          intentId: "intent_1",
          userId: "user_1",
          provider: "bkash",
          bdtAmount: 25,
          status: "initiated",
          mintedCredits: 0,
          paymentLedgerCredits: 0,
          createdAt: "2026-03-11T10:00:00.000Z",
        },
      ],
      events: [
        {
          eventKey: "bkash:txn_1",
          intentId: "intent_1",
          provider: "bkash",
          providerTxnId: "txn_1",
          verified: true,
          createdAt: "2026-03-11T10:05:00.000Z",
        },
      ],
    };

    const result = reconcilePaymentDrift(snapshot);

    expect(result.summary.totalFindings).toBe(1);
    expect(result.summary.verifiedEventWithoutCreditedIntent).toBe(1);
    expect(result.findings).toEqual([
      expect.objectContaining({
        kind: "verified_event_without_credited_intent",
        intentId: "intent_1",
      }),
    ]);
  });

  it("flags credited intents without verified payment events", () => {
    const snapshot: PaymentReconciliationSnapshot = {
      intents: [
        {
          intentId: "intent_2",
          userId: "user_2",
          provider: "sslcommerz",
          bdtAmount: 10,
          status: "credited",
          mintedCredits: 1000,
          paymentLedgerCredits: 1000,
          createdAt: "2026-03-11T11:00:00.000Z",
        },
      ],
      events: [],
    };

    const result = reconcilePaymentDrift(snapshot);

    expect(result.summary.totalFindings).toBe(1);
    expect(result.summary.creditedIntentWithoutVerifiedEvent).toBe(1);
    expect(result.findings).toEqual([
      expect.objectContaining({
        kind: "credited_intent_without_verified_event",
        intentId: "intent_2",
      }),
    ]);
  });

  it("flags credited intents whose minted credits do not match the payment amount", () => {
    const snapshot: PaymentReconciliationSnapshot = {
      intents: [
        {
          intentId: "intent_3",
          userId: "user_3",
          provider: "bkash",
          bdtAmount: 15,
          status: "credited",
          mintedCredits: 1200,
          paymentLedgerCredits: 1200,
          createdAt: "2026-03-11T12:00:00.000Z",
        },
      ],
      events: [
        {
          eventKey: "bkash:txn_3",
          intentId: "intent_3",
          provider: "bkash",
          providerTxnId: "txn_3",
          verified: true,
          createdAt: "2026-03-11T12:05:00.000Z",
        },
      ],
    };

    const result = reconcilePaymentDrift(snapshot);

    expect(result.summary.totalFindings).toBe(1);
    expect(result.summary.creditedAmountMismatch).toBe(1);
    expect(result.findings).toEqual([
      expect.objectContaining({
        kind: "credited_amount_mismatch",
        intentId: "intent_3",
        expectedMintedCredits: 1500,
        actualMintedCredits: 1200,
      }),
    ]);
  });

  it("flags credited intents whose ledger credits do not match the payment amount", () => {
    const snapshot: PaymentReconciliationSnapshot = {
      intents: [
        {
          intentId: "intent_ledger_mismatch",
          userId: "user_ledger",
          provider: "bkash",
          bdtAmount: 15,
          status: "credited",
          mintedCredits: 1500,
          paymentLedgerCredits: 1400,
          createdAt: "2026-03-11T12:00:00.000Z",
        },
      ],
      events: [
        {
          eventKey: "bkash:txn_ledger_mismatch",
          intentId: "intent_ledger_mismatch",
          provider: "bkash",
          providerTxnId: "txn_ledger_mismatch",
          verified: true,
          createdAt: "2026-03-11T12:05:00.000Z",
        },
      ],
    };

    const result = reconcilePaymentDrift(snapshot);

    expect(result.summary.totalFindings).toBe(1);
    expect(result.summary.creditedAmountMismatch).toBe(1);
    expect(result.findings).toEqual([
      expect.objectContaining({
        kind: "credited_amount_mismatch",
        intentId: "intent_ledger_mismatch",
        expectedMintedCredits: 1500,
        actualMintedCredits: 1500,
        actualLedgerCredits: 1400,
      }),
    ]);
  });

  it("loads a recent snapshot from the store before reconciling", async () => {
    const snapshot: PaymentReconciliationSnapshot = {
      intents: [
        {
          intentId: "intent_4",
          userId: "user_4",
          provider: "bkash",
          bdtAmount: 20,
          status: "credited",
          mintedCredits: 2000,
          paymentLedgerCredits: 2000,
          createdAt: "2026-03-11T09:30:00.000Z",
        },
      ],
      events: [],
    };
    const listRecentSnapshot = async (since: Date) => {
      expect(since.toISOString()).toBe("2026-03-11T08:00:00.000Z");
      return snapshot;
    };

    const service = new PaymentReconciliationService(
      { listRecentSnapshot },
      () => new Date("2026-03-11T12:00:00.000Z"),
    );

    const result = await service.reconcileRecentPayments(4);

    expect(result.summary.totalFindings).toBe(1);
    expect(result.summary.creditedIntentWithoutVerifiedEvent).toBe(1);
  });

  it("flags credited intents without payment ledger evidence", () => {
    const snapshot: PaymentReconciliationSnapshot = {
      intents: [
        {
          intentId: "intent_5",
          userId: "user_5",
          provider: "bkash",
          bdtAmount: 12,
          status: "credited",
          mintedCredits: 1200,
          paymentLedgerCredits: 0,
          createdAt: "2026-03-11T12:00:00.000Z",
        },
      ],
      events: [
        {
          eventKey: "bkash:txn_5",
          intentId: "intent_5",
          provider: "bkash",
          providerTxnId: "txn_5",
          verified: true,
          createdAt: "2026-03-11T12:05:00.000Z",
        },
      ],
    };

    const result = reconcilePaymentDrift(snapshot);

    expect(result.summary.totalFindings).toBe(1);
    expect(result.summary.missingPaymentLedgerEntry).toBe(1);
    expect(result.findings).toEqual([
      expect.objectContaining({
        kind: "missing_payment_ledger_entry",
        intentId: "intent_5",
      }),
    ]);
  });

  it("does not flag a recent intent when its matching verified event falls just outside the lookback window", async () => {
    const listRecentSnapshot = async (since: Date) => {
      expect(since.toISOString()).toBe("2026-03-11T08:00:00.000Z");
      return {
        intents: [
          {
            intentId: "intent_6",
            userId: "user_6",
            provider: "bkash",
            bdtAmount: 8,
            status: "credited",
            mintedCredits: 800,
            paymentLedgerCredits: 800,
            createdAt: "2026-03-11T08:01:00.000Z",
          },
        ],
        events: [
          {
            eventKey: "bkash:txn_6",
            intentId: "intent_6",
            provider: "bkash",
            providerTxnId: "txn_6",
            verified: true,
            createdAt: "2026-03-11T07:59:00.000Z",
          },
        ],
      };
    };
    const service = new PaymentReconciliationService(
      { listRecentSnapshot },
      () => new Date("2026-03-11T12:00:00.000Z"),
    );

    const result = await service.reconcileRecentPayments(4);

    expect(result.summary.totalFindings).toBe(0);
  });

  it("does not flag correctly credited decimal amounts because of floating-point underflow", () => {
    const snapshot: PaymentReconciliationSnapshot = {
      intents: [
        {
          intentId: "intent_7",
          userId: "user_7",
          provider: "bkash",
          bdtAmount: 19.99,
          status: "credited",
          mintedCredits: 1999,
          paymentLedgerCredits: 1999,
          createdAt: "2026-03-11T12:00:00.000Z",
        },
      ],
      events: [
        {
          eventKey: "bkash:txn_7",
          intentId: "intent_7",
          provider: "bkash",
          providerTxnId: "txn_7",
          verified: true,
          createdAt: "2026-03-11T12:05:00.000Z",
        },
      ],
    };

    const result = reconcilePaymentDrift(snapshot);

    expect(result.summary.totalFindings).toBe(0);
  });
});
