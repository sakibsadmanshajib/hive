"use client";

import { useState } from "react";

import type { BudgetThreshold } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import { formatCredits } from "@/lib/format/credits";

interface BudgetAlertBannerProps {
  threshold: BudgetThreshold | null;
  // null when the balance could not be read. It is deliberately not a number:
  // the layout used to substitute 0 for an unreadable balance, and 0 is below
  // every threshold a customer can set, so an outage rendered as a real
  // threshold breach on every console route (issue #494).
  currentBalance: number | null;
}

export function BudgetAlertBanner({
  threshold,
  currentBalance,
}: BudgetAlertBannerProps) {
  const [dismissed, setDismissed] = useState(false);

  if (!threshold || threshold.alert_dismissed || dismissed) {
    return null;
  }

  // Nothing truthful to say about a balance we do not have. The banner exists
  // to tell the customer where they stand; it cannot do that from a guess.
  if (currentBalance === null) {
    return null;
  }

  const isApproaching =
    currentBalance <= threshold.threshold_credits * 1.1 &&
    currentBalance > threshold.threshold_credits;
  const isCrossed = currentBalance <= threshold.threshold_credits;

  if (!isApproaching && !isCrossed) {
    return null;
  }

  const formatted = formatCredits(threshold.threshold_credits);
  const lead = isCrossed
    ? "Your balance has reached or dropped below your alert threshold of "
    : "Your balance is approaching your alert threshold of ";

  async function handleDismiss() {
    try {
      await fetch("/api/budget", { method: "DELETE" });
    } catch {
      // Best-effort dismiss — hide locally even if network fails.
    }
    setDismissed(true);
  }

  return (
    <div
      role="status"
      className="flex items-center justify-between gap-3 border-b border-[var(--color-warning)]/30 bg-[var(--color-warning-soft)] px-6 py-2 text-xs text-[var(--color-warning)]"
    >
      <p className="m-0">
        {lead}
        <span className="metric">{formatted}</span> credits.
      </p>
      <Button
        type="button"
        variant="secondary"
        size="sm"
        onClick={() => void handleDismiss()}
      >
        Dismiss
      </Button>
    </div>
  );
}
