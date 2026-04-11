"use client";

import { useState } from "react";
import type { BudgetThreshold } from "@/lib/control-plane/client";

interface BudgetAlertBannerProps {
  threshold: BudgetThreshold | null;
  currentBalance: number;
}

export function BudgetAlertBanner({ threshold, currentBalance }: BudgetAlertBannerProps) {
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

  const message = isCrossed
    ? `Your balance has dropped below your alert threshold of ${threshold.threshold_credits.toLocaleString()} Hive Credits.`
    : `Your balance is approaching your alert threshold of ${threshold.threshold_credits.toLocaleString()} Hive Credits.`;

  async function handleDismiss() {
    try {
      await fetch("/api/budget", { method: "DELETE" });
    } catch {
      // Best-effort dismiss — hide locally even if network fails
    }
    setDismissed(true);
  }

  return (
    <div
      style={{
        maxWidth: "36rem",
        margin: "0 auto",
        backgroundColor: "#fef9c3",
        border: "1px solid #fde047",
        borderRadius: "0.5rem",
        padding: "0.75rem 1rem",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: "1rem",
      }}
    >
      <p style={{ margin: 0, fontSize: "0.875rem", color: "#854d0e" }}>{message}</p>
      <button
        onClick={() => void handleDismiss()}
        style={{
          flexShrink: 0,
          padding: "0.25rem 0.625rem",
          fontSize: "0.75rem",
          fontWeight: 600,
          borderRadius: "0.375rem",
          border: "1px solid #fde047",
          backgroundColor: "transparent",
          color: "#854d0e",
          cursor: "pointer",
          whiteSpace: "nowrap",
        }}
      >
        Dismiss alert
      </button>
    </div>
  );
}
