import { CreditLedger } from "./credits-ledger";
import { bdtToCredits } from "./credits-conversion";

interface CreditTopUpService {
  topUp(userId: string, bdtAmount: number): unknown;
}

export interface PaymentIntent {
  intentId: string;
  userId: string;
  provider: string;
  bdtAmount: number;
  status: string;
  mintedCredits: number;
}

export interface PaymentStore {
  recordPaymentIntent(intentId: string, userId: string, bdtAmount: number, status: string): void;
  recordPaymentEvent(
    eventId: string,
    intentId: string,
    provider: string,
    providerTxnId: string,
    verified: boolean,
  ): void;
  markIntentCredited(intentId: string, credits: number): void;
}

export class PaymentService {
  private readonly ledger?: CreditLedger;
  private readonly credits?: CreditTopUpService;
  private readonly store?: PaymentStore;
  private readonly intents = new Map<string, PaymentIntent>();
  private readonly processedProviderTxns = new Set<string>();

  constructor(ledgerOrCredits: CreditLedger | CreditTopUpService, store?: PaymentStore) {
    if (ledgerOrCredits instanceof CreditLedger) {
      this.ledger = ledgerOrCredits;
    } else {
      this.credits = ledgerOrCredits;
    }
    this.store = store;
  }

  createIntent(intentId: string, userId: string, bdtAmount: number): PaymentIntent;
  createIntent(input: { userId: string; provider: string; bdtAmount: number }): PaymentIntent;
  createIntent(
    intentIdOrInput: string | { userId: string; provider: string; bdtAmount: number },
    userId?: string,
    bdtAmount?: number,
  ): PaymentIntent {
    const intentId =
      typeof intentIdOrInput === "string" ? intentIdOrInput : `intent_${Math.random().toString(36).slice(2, 12)}`;
    const resolvedUserId = typeof intentIdOrInput === "string" ? userId ?? "user-1" : intentIdOrInput.userId;
    const resolvedAmount = typeof intentIdOrInput === "string" ? bdtAmount ?? 0 : intentIdOrInput.bdtAmount;
    const resolvedProvider = typeof intentIdOrInput === "string" ? "bkash" : intentIdOrInput.provider;

    const intent: PaymentIntent = {
      intentId,
      userId: resolvedUserId,
      provider: resolvedProvider,
      bdtAmount: resolvedAmount,
      status: "initiated",
      mintedCredits: 0,
    };
    this.intents.set(intentId, intent);
    this.store?.recordPaymentIntent(intentId, resolvedUserId, resolvedAmount, "initiated");
    return intent;
  }

  handleVerifiedEvent(provider: string, providerTxnId: string, intentId: string, verified: boolean): void {
    const eventKey = `${provider}:${providerTxnId}`;
    if (this.processedProviderTxns.has(eventKey)) {
      return;
    }
    this.processedProviderTxns.add(eventKey);

    this.store?.recordPaymentEvent(eventKey, intentId, provider, providerTxnId, verified);

    const intent = this.intents.get(intentId);
    if (intent === undefined) {
      return;
    }
    if (!verified) {
      intent.status = "failed";
      return;
    }
    if (intent.status === "credited") {
      return;
    }

    const credits = bdtToCredits(intent.bdtAmount);
    if (this.ledger) {
      this.ledger.mintPurchased(intent.userId, credits, intent.intentId);
      this.store?.markIntentCredited(intent.intentId, credits);
      intent.status = "credited";
      intent.mintedCredits = credits;
      return;
    }

    if (this.credits) {
      this.credits.topUp(intent.userId, intent.bdtAmount);
      this.store?.markIntentCredited(intent.intentId, credits);
      intent.status = "credited";
      intent.mintedCredits = credits;
    }
  }

  applyWebhook(payload: { provider: string; intent_id: string; provider_txn_id: string; verified: boolean }): PaymentIntent | undefined {
    this.handleVerifiedEvent(payload.provider, payload.provider_txn_id, payload.intent_id, payload.verified);
    return this.intents.get(payload.intent_id);
  }
}
