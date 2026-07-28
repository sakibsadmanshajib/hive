"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { CheckCircle2, CircleSlash, Loader2, XCircle } from "lucide-react";

import type { CheckoutIntent } from "@/lib/control-plane/client";
import {
  BILLING_PATH,
  RETURN_HINT_CANCELLED,
  isCheckoutReturnState,
  type CheckoutReturnState,
} from "@/lib/payments/checkout-return";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { formatCredits } from "@/lib/format/credits";

interface CheckoutReturnStatusProps {
  /** Authoritative intent state, read server-side from the control-plane. */
  initial: CheckoutIntent;
  /**
   * Copy hint from the provider's cancel URL, or null.
   *
   * TRUST BOUNDARY: the hint only ever selects wording while `initial.state` is
   * still pending. It cannot claim success, failure, or a credit.
   */
  hint: string | null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

const POLL_INTERVAL_MS = 4000;
// Roughly two minutes of polling. BD rails confirm on a server-side loop that
// runs every 60 seconds, so a real confirmation normally lands well inside this.
const MAX_POLLS = 30;

function isTerminal(state: CheckoutReturnState): boolean {
  return state !== "pending";
}

/**
 * CheckoutReturnStatus renders the four outcomes a returning payer can see.
 *
 * BD regulatory rule: no currency amount, no FX rate, and no exchange language
 * appears on this surface. Credits are the only quantity shown, because credits
 * are currency free.
 */
export function CheckoutReturnStatus({ initial, hint }: CheckoutReturnStatusProps) {
  const [intent, setIntent] = useState<CheckoutIntent>(initial);
  const [pollsExhausted, setPollsExhausted] = useState(false);
  const pollCount = useRef(0);

  const refresh = useCallback(async (): Promise<CheckoutReturnState | null> => {
    const query = new URLSearchParams({ payment_intent_id: intent.payment_intent_id });
    const response = await fetch(`/api/console/checkout/intent?${query.toString()}`, {
      credentials: "include",
      cache: "no-store",
    });
    if (!response.ok) return null;

    const payload: unknown = await response.json();
    if (!isRecord(payload)) return null;
    const record = payload;
    if (!isCheckoutReturnState(record.state)) return null;

    const next: CheckoutIntent = {
      payment_intent_id:
        typeof record.payment_intent_id === "string"
          ? record.payment_intent_id
          : intent.payment_intent_id,
      rail: typeof record.rail === "string" ? record.rail : intent.rail,
      status: typeof record.status === "string" ? record.status : intent.status,
      state: record.state,
      credits: typeof record.credits === "number" ? record.credits : intent.credits,
    };
    setIntent(next);
    return next.state;
  }, [intent.payment_intent_id, intent.rail, intent.status, intent.credits]);

  useEffect(() => {
    if (isTerminal(intent.state) || pollsExhausted) return;

    let cancelled = false;
    const timer = setTimeout(() => {
      void (async () => {
        pollCount.current += 1;
        const state = await refresh();
        if (cancelled) return;
        if (pollCount.current >= MAX_POLLS && (state === null || state === "pending")) {
          setPollsExhausted(true);
        }
      })();
    }, POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // `intent` is in the dependency list so a resolved poll stops the loop.
  }, [intent, pollsExhausted, refresh]);

  const credits = formatCredits(intent.credits);

  if (intent.state === "success") {
    return (
      <ReturnCard
        tone="success"
        icon={<CheckCircle2 size={20} aria-hidden="true" />}
        title="Payment complete"
        body={`${credits} credits have been added to your balance.`}
      />
    );
  }

  if (intent.state === "failed") {
    return (
      <ReturnCard
        tone="danger"
        icon={<XCircle size={20} aria-hidden="true" />}
        title="Payment did not go through"
        body="No credits were added and nothing was charged. You can try the purchase again from the billing page."
      />
    );
  }

  if (intent.state === "cancelled") {
    return (
      <ReturnCard
        tone="muted"
        icon={<CircleSlash size={20} aria-hidden="true" />}
        title="Payment cancelled"
        body="Nothing was charged and your balance is unchanged. Start again from the billing page whenever you are ready."
      />
    );
  }

  // Pending. This is the common real case: the browser gets back before the
  // provider's webhook has landed, so the outcome is genuinely not decided yet.
  const cancelledHint = hint === RETURN_HINT_CANCELLED;
  return (
    <ReturnCard
      tone="muted"
      icon={<Loader2 size={20} aria-hidden="true" className="animate-spin" />}
      title={cancelledHint ? "Nothing confirmed yet" : "Confirming your payment"}
      body={
        cancelledHint
          ? `You left the payment page before it finished. Nothing has been confirmed, and your balance is unchanged unless the payment completes.`
          : pollsExhausted
            ? `This is taking longer than usual. Your ${credits} credits will appear on your balance as soon as the payment is confirmed. Nothing further is needed from you.`
            : `Waiting for confirmation. Your ${credits} credits will appear on your balance as soon as the payment is confirmed. You can safely leave this page.`
      }
      live
    />
  );
}

interface ReturnCardProps {
  tone: "success" | "danger" | "muted";
  icon: React.ReactNode;
  title: string;
  body: string;
  live?: boolean;
}

const TONE_CLASS: Record<ReturnCardProps["tone"], string> = {
  success: "text-[var(--color-success)]",
  danger: "text-[var(--color-danger)]",
  muted: "text-[var(--color-ink-3)]",
};

function ReturnCard({ tone, icon, title, body, live }: ReturnCardProps) {
  return (
    <Card>
      <CardContent className="flex flex-col gap-4 px-6 py-7">
        <div className={`flex items-center gap-2 ${TONE_CLASS[tone]}`}>
          {icon}
          <h1 className="font-display text-xl text-[var(--color-ink)]">{title}</h1>
        </div>
        <p
          className="text-sm text-[var(--color-ink-2)]"
          {...(live ? { role: "status", "aria-live": "polite" } : {})}
        >
          {body}
        </p>
        <div className="flex items-center gap-3">
          <Link href={BILLING_PATH} className={buttonVariants({ variant: "accent", size: "md" })}>
            Back to billing
          </Link>
        </div>
      </CardContent>
    </Card>
  );
}
