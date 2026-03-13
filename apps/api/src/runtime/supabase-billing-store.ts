import type { SupabaseClient } from "@supabase/supabase-js";
import type { CreditBalance } from "../domain/types";
import type { PersistentPaymentIntent } from "../domain/types";
import type { PaymentReconciliationSnapshot } from "../domain/types";

type CreditAccountRow = {
  user_id: string;
  available_credits: number;
  purchased_credits: number;
  promo_credits: number;
};

type PaymentIntentRow = {
  intent_id: string;
  user_id: string;
  provider: "bkash" | "sslcommerz";
  bdt_amount: number;
  status: "initiated" | "credited" | "failed";
  minted_credits: number;
};

type PaymentIntentSnapshotRow = PaymentIntentRow & {
  created_at: string;
};

type PaymentLedgerRow = {
  reference_id: string;
  credits: number;
};

type PaymentEventRow = {
  event_key: string;
  intent_id: string;
  provider: "bkash" | "sslcommerz";
  provider_txn_id: string;
  verified: boolean;
  created_at: string;
};

export class SupabaseBillingStore {
  constructor(private readonly supabase: SupabaseClient) { }

  async getBalance(userId: string): Promise<CreditBalance> {
    await this.ensureCreditAccount(userId);
    const { data, error } = await this.supabase
      .from("credit_accounts")
      .select("user_id, available_credits, purchased_credits, promo_credits")
      .eq("user_id", userId)
      .maybeSingle<CreditAccountRow>();
    if (error) {
      throw new Error(`failed to read credit balance: ${error.message}`);
    }
    return {
      userId,
      availableCredits: Number(data?.available_credits ?? 0),
      purchasedCredits: Number(data?.purchased_credits ?? 0),
      promoCredits: Number(data?.promo_credits ?? 0),
    };
  }

  async consumeCredits(userId: string, credits: number, referenceId: string): Promise<boolean> {
    const { data, error } = await this.supabase.rpc("consume_credits", {
      p_user_id: userId,
      p_credits: credits,
      p_reference_id: referenceId,
    });
    if (error) {
      throw new Error(`failed to consume credits via rpc: ${error.message}`);
    }
    return Boolean(data);
  }

  async refundCredits(userId: string, credits: number, referenceId: string): Promise<CreditBalance> {
    const { data, error } = await this.supabase.rpc("refund_credits", {
      p_user_id: userId,
      p_credits: credits,
      p_reference_id: referenceId,
    });
    if (error) {
      throw new Error(`failed to refund credits via rpc: ${error.message}`);
    }
    if (!data) {
      throw new Error("refund credits rpc rejected the refund request");
    }
    return this.getBalance(userId);
  }

  async topUpCredits(userId: string, credits: number, referenceId: string): Promise<CreditBalance> {
    await this.ensureCreditAccount(userId);
    const balance = await this.getBalance(userId);

    const { error: updateError } = await this.supabase
      .from("credit_accounts")
      .update({
        available_credits: balance.availableCredits + credits,
        purchased_credits: balance.purchasedCredits + credits,
      })
      .eq("user_id", userId);
    if (updateError) {
      throw new Error(`failed to top up credits: ${updateError.message}`);
    }

    const { error: ledgerError } = await this.supabase.from("credit_ledger").upsert(
      {
        user_id: userId,
        entry_type: "credit",
        credits,
        reference_type: "payment",
        reference_id: referenceId,
      },
      { onConflict: "reference_type,reference_id", ignoreDuplicates: true },
    );
    if (ledgerError) {
      throw new Error(`failed to write credit ledger entry: ${ledgerError.message}`);
    }

    return this.getBalance(userId);
  }

  async createPaymentIntent(intent: PersistentPaymentIntent): Promise<void> {
    const { error } = await this.supabase.from("payment_intents").insert({
      intent_id: intent.intentId,
      user_id: intent.userId,
      provider: intent.provider,
      bdt_amount: intent.bdtAmount,
      status: intent.status,
      minted_credits: intent.mintedCredits,
    });
    if (error) {
      throw new Error(`failed to create payment intent: ${error.message}`);
    }
  }

  async getPaymentIntent(intentId: string): Promise<PersistentPaymentIntent | undefined> {
    const { data, error } = await this.supabase
      .from("payment_intents")
      .select("intent_id, user_id, provider, bdt_amount, status, minted_credits")
      .eq("intent_id", intentId)
      .maybeSingle<PaymentIntentRow>();
    if (error) {
      throw new Error(`failed to read payment intent: ${error.message}`);
    }
    if (!data) {
      return undefined;
    }
    return {
      intentId: data.intent_id,
      userId: data.user_id,
      provider: data.provider,
      bdtAmount: Number(data.bdt_amount),
      status: data.status,
      mintedCredits: Number(data.minted_credits),
    };
  }

  async recordPaymentEvent(
    eventKey: string,
    intentId: string,
    provider: string,
    providerTxnId: string,
    verified: boolean,
  ): Promise<boolean> {
    const { data: existing, error: lookupError } = await this.supabase
      .from("payment_events")
      .select("event_key")
      .eq("event_key", eventKey)
      .maybeSingle<{ event_key: string }>();
    if (lookupError) {
      throw new Error(`failed to check payment event idempotency: ${lookupError.message}`);
    }
    if (existing) {
      return false;
    }

    const { error } = await this.supabase.from("payment_events").insert({
      event_key: eventKey,
      intent_id: intentId,
      provider,
      provider_txn_id: providerTxnId,
      verified,
    });
    if (error) {
      throw new Error(`failed to record payment event: ${error.message}`);
    }
    return true;
  }

  async markPaymentCredited(intentId: string, mintedCredits: number, status: "credited" | "failed"): Promise<void> {
    const { error } = await this.supabase
      .from("payment_intents")
      .update({ status, minted_credits: mintedCredits })
      .eq("intent_id", intentId);
    if (error) {
      throw new Error(`failed to mark payment intent status: ${error.message}`);
    }
  }

  async claimPaymentIntent(
    intentId: string,
    provider: string,
    providerTxnId: string,
  ): Promise<{ success: boolean; intent?: PersistentPaymentIntent; error?: string }> {
    const { data, error } = await this.supabase.rpc("claim_payment_intent", {
      p_intent_id: intentId,
      p_provider: provider,
      p_provider_txn_id: providerTxnId,
    });
    if (error) {
      throw new Error(`failed to claim payment intent via rpc: ${error.message}`);
    }
    const result = data as { success: boolean; intent?: PaymentIntentRow; error?: string };
    if (!result.success || !result.intent) {
      return { success: false, error: result.error ?? "claim rejected" };
    }
    return {
      success: true,
      intent: {
        intentId: result.intent.intent_id,
        userId: result.intent.user_id,
        provider: result.intent.provider,
        bdtAmount: Number(result.intent.bdt_amount),
        status: result.intent.status,
        mintedCredits: Number(result.intent.minted_credits),
      },
    };
  }

  async listRecentSnapshot(since: Date): Promise<PaymentReconciliationSnapshot> {
    const sinceIso = since.toISOString();
    const { data: recentIntents, error: recentIntentsError } = await this.supabase
      .from("payment_intents")
      .select("intent_id")
      .gte("created_at", sinceIso)
      .order("created_at", { ascending: false });
    if (recentIntentsError) {
      throw new Error(`failed to read recent payment intents: ${recentIntentsError.message}`);
    }

    const { data: recentEvents, error: recentEventsError } = await this.supabase
      .from("payment_events")
      .select("intent_id")
      .gte("created_at", sinceIso)
      .order("created_at", { ascending: false });
    if (recentEventsError) {
      throw new Error(`failed to read recent payment events: ${recentEventsError.message}`);
    }

    const { data: recentLedger, error: recentLedgerError } = await this.supabase
      .from("credit_ledger")
      .select("reference_id")
      .eq("reference_type", "payment")
      .gte("created_at", sinceIso)
      .order("created_at", { ascending: false });
    if (recentLedgerError) {
      throw new Error(`failed to read recent payment ledger entries: ${recentLedgerError.message}`);
    }

    const affectedIntentIds = [
      ...new Set(
        [
          ...(recentIntents ?? []).map((row) => String((row as { intent_id: string }).intent_id)),
          ...(recentEvents ?? []).map((row) => String((row as { intent_id: string }).intent_id)),
          ...(recentLedger ?? []).map((row) => String((row as { reference_id: string }).reference_id)),
        ].filter((value) => value.length > 0),
      ),
    ];

    if (affectedIntentIds.length === 0) {
      return { intents: [], events: [] };
    }

    const { data: intents, error: intentsError } = await this.supabase
      .from("payment_intents")
      .select("intent_id, user_id, provider, bdt_amount, status, minted_credits, created_at")
      .in("intent_id", affectedIntentIds)
      .order("created_at", { ascending: false });
    if (intentsError) {
      throw new Error(`failed to read reconciliation payment intents: ${intentsError.message}`);
    }

    const { data: events, error: eventsError } = await this.supabase
      .from("payment_events")
      .select("event_key, intent_id, provider, provider_txn_id, verified, created_at")
      .in("intent_id", affectedIntentIds)
      .order("created_at", { ascending: false });
    if (eventsError) {
      throw new Error(`failed to read reconciliation payment events: ${eventsError.message}`);
    }

    const { data: ledgerEntries, error: ledgerEntriesError } = await this.supabase
      .from("credit_ledger")
      .select("reference_id, credits")
      .eq("reference_type", "payment")
      .in("reference_id", affectedIntentIds);
    if (ledgerEntriesError) {
      throw new Error(`failed to read reconciliation payment ledger entries: ${ledgerEntriesError.message}`);
    }

    const ledgerCreditsByIntentId = new Map<string, number>();
    for (const row of ledgerEntries ?? []) {
      const entry = row as PaymentLedgerRow;
      ledgerCreditsByIntentId.set(
        String(entry.reference_id),
        (ledgerCreditsByIntentId.get(String(entry.reference_id)) ?? 0) + Number(entry.credits),
      );
    }

    return {
      intents: (intents ?? []).map((intent) => ({
        intentId: String((intent as PaymentIntentSnapshotRow).intent_id),
        userId: String((intent as PaymentIntentSnapshotRow).user_id),
        provider: (intent as PaymentIntentSnapshotRow).provider,
        bdtAmount: Number((intent as PaymentIntentSnapshotRow).bdt_amount),
        status: (intent as PaymentIntentSnapshotRow).status,
        mintedCredits: Number((intent as PaymentIntentSnapshotRow).minted_credits),
        paymentLedgerCredits: ledgerCreditsByIntentId.get(String((intent as PaymentIntentSnapshotRow).intent_id)) ?? 0,
        createdAt: new Date((intent as PaymentIntentSnapshotRow).created_at).toISOString(),
      })),
      events: (events ?? []).map((event) => ({
        eventKey: String((event as PaymentEventRow).event_key),
        intentId: String((event as PaymentEventRow).intent_id),
        provider: (event as PaymentEventRow).provider,
        providerTxnId: String((event as PaymentEventRow).provider_txn_id),
        verified: Boolean((event as PaymentEventRow).verified),
        createdAt: new Date((event as PaymentEventRow).created_at).toISOString(),
      })),
    };
  }

  private async ensureCreditAccount(userId: string): Promise<void> {
    const { error } = await this.supabase
      .from("credit_accounts")
      .upsert({ user_id: userId }, { onConflict: "user_id", ignoreDuplicates: true });
    if (error) {
      throw new Error(`failed to ensure credit account row: ${error.message}`);
    }
  }
}
