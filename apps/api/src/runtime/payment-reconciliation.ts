import type {
  PaymentReconciliationFinding,
  PaymentReconciliationResult,
  PaymentReconciliationSnapshot,
} from "../domain/types";

type ReconciliationSnapshotStore = {
  listRecentSnapshot(since: Date): Promise<PaymentReconciliationSnapshot>;
};

function buildEmptyResult(): PaymentReconciliationResult {
  return {
    summary: {
      totalFindings: 0,
      verifiedEventWithoutCreditedIntent: 0,
      creditedIntentWithoutVerifiedEvent: 0,
      creditedAmountMismatch: 0,
      missingPaymentLedgerEntry: 0,
    },
    findings: [],
  };
}

export function reconcilePaymentDrift(snapshot: PaymentReconciliationSnapshot): PaymentReconciliationResult {
  const result = buildEmptyResult();
  const findings: PaymentReconciliationFinding[] = [];
  const intentsById = new Map(snapshot.intents.map((intent) => [intent.intentId, intent]));
  const verifiedIntentIds = new Set(
    snapshot.events.filter((event) => event.verified).map((event) => event.intentId),
  );

  for (const event of snapshot.events) {
    if (!event.verified) {
      continue;
    }

    const intent = intentsById.get(event.intentId);
    if (!intent || intent.status !== "credited") {
      findings.push({
        kind: "verified_event_without_credited_intent",
        intentId: event.intentId,
        provider: event.provider,
        providerTxnId: event.providerTxnId,
      });
      result.summary.verifiedEventWithoutCreditedIntent += 1;
    }
  }

  for (const intent of snapshot.intents) {
    if (intent.status !== "credited") {
      continue;
    }

    if (!verifiedIntentIds.has(intent.intentId)) {
      findings.push({
        kind: "credited_intent_without_verified_event",
        intentId: intent.intentId,
        provider: intent.provider,
      });
      result.summary.creditedIntentWithoutVerifiedEvent += 1;
    }

    const expectedMintedCredits = Math.trunc(intent.bdtAmount * 100);
    if (intent.paymentLedgerCredits === 0) {
      findings.push({
        kind: "missing_payment_ledger_entry",
        intentId: intent.intentId,
        provider: intent.provider,
      });
      result.summary.missingPaymentLedgerEntry += 1;
    }

    if (
      intent.mintedCredits !== expectedMintedCredits ||
      (intent.paymentLedgerCredits > 0 && intent.paymentLedgerCredits !== expectedMintedCredits)
    ) {
      findings.push({
        kind: "credited_amount_mismatch",
        intentId: intent.intentId,
        provider: intent.provider,
        expectedMintedCredits,
        actualMintedCredits: intent.mintedCredits,
        actualLedgerCredits: intent.paymentLedgerCredits,
      });
      result.summary.creditedAmountMismatch += 1;
    }
  }

  result.findings = findings;
  result.summary.totalFindings = findings.length;
  return result;
}

export class PaymentReconciliationService {
  constructor(
    private readonly store: ReconciliationSnapshotStore,
    private readonly nowFn: () => Date = () => new Date(),
  ) { }

  async reconcileRecentPayments(lookbackHours: number): Promise<PaymentReconciliationResult> {
    const since = new Date(this.nowFn().getTime() - lookbackHours * 60 * 60 * 1000);
    const snapshot = await this.store.listRecentSnapshot(since);
    return reconcilePaymentDrift(snapshot);
  }
}
