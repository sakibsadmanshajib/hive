"use client";

import { useState } from "react";

import type { BudgetThreshold } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import { formatCredits } from "@/lib/format/credits";

interface BudgetAlertBannerProps {
  threshold: BudgetThreshold | null;
  currentBalance: number;
}

export function BudgetAlertBanner({
  threshold,
  currentBalance,
}: BudgetAlertBannerProps) {
  const [dismissed, setDismissed] = useState(false);

  if (!threshold || threshold.alert_dismissed || dismissed) {
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
  const message = isCrossed
    ? `Your balance has dropped below your alert threshold of ${formatted} credits.`
    : `Your balance is approaching your alert threshold of ${formatted} credits.`;

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
      <p className="m-0 tabular-nums">{message}</p>
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
