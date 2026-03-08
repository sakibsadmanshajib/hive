import type { SupabaseClient } from "@supabase/supabase-js";
import type { CreditBalance } from "../domain/types";
import type { PersistentPaymentIntent } from "../domain/types";

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
    await this.ensureCreditAccount(userId);
    const balance = await this.getBalance(userId);
    if (balance.availableCredits < credits) {
      return false;
    }

    const purchasedDebit = Math.min(balance.purchasedCredits, credits);
    const { error: updateError } = await this.supabase
      .from("credit_accounts")
      .update({
        available_credits: balance.availableCredits - credits,
        purchased_credits: balance.purchasedCredits - purchasedDebit,
      })
      .eq("user_id", userId);
    if (updateError) {
      throw new Error(`failed to consume credits: ${updateError.message}`);
    }

    const { error: ledgerError } = await this.supabase.from("credit_ledger").upsert(
      {
        user_id: userId,
        entry_type: "debit",
        credits,
        reference_type: "usage",
        reference_id: referenceId,
      },
      { onConflict: "reference_type,reference_id", ignoreDuplicates: true },
    );
    if (ledgerError) {
      throw new Error(`failed to write debit ledger entry: ${ledgerError.message}`);
    }

    return true;
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

  private async ensureCreditAccount(userId: string): Promise<void> {
    const { error } = await this.supabase
      .from("credit_accounts")
      .upsert({ user_id: userId }, { onConflict: "user_id", ignoreDuplicates: true });
    if (error) {
      throw new Error(`failed to ensure credit account row: ${error.message}`);
    }
  }
}
