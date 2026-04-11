"use client";

import { useState } from "react";
import type { BudgetThreshold } from "@/lib/control-plane/client";

interface BudgetAlertFormProps {
  currentThreshold: BudgetThreshold | null;
}

export function BudgetAlertForm({ currentThreshold }: BudgetAlertFormProps) {
  const [thresholdCredits, setThresholdCredits] = useState<string>(
    currentThreshold ? String(currentThreshold.threshold_credits) : ""
  );
  const [loading, setLoading] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const parsed = parseInt(thresholdCredits, 10);
    if (isNaN(parsed) || parsed <= 0) {
      setError("Please enter a valid positive number.");
      return;
    }

    setLoading(true);
    setError(null);
    setSaved(false);

    try {
      const response = await fetch("/api/budget", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ threshold_credits: parsed }),
      });

      if (!response.ok) {
        const data: unknown = await response.json();
        const errMsg =
          data !== null &&
          typeof data === "object" &&
          "error" in data &&
          typeof (data as { error: unknown }).error === "string"
            ? (data as { error: string }).error
            : "Failed to save alert.";
        setError(errMsg);
        return;
      }

      setSaved(true);
    } catch {
      setError("Network error. Please try again.");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div style={{ marginTop: "2rem", paddingTop: "2rem", borderTop: "1px solid #e5e7eb" }}>
      <h2 style={{ margin: "0 0 1rem", fontSize: "1.25rem", fontWeight: 700 }}>Spend alerts</h2>
      <form onSubmit={handleSubmit}>
        <div style={{ marginBottom: "0.75rem" }}>
          <label
            htmlFor="threshold-credits"
            style={{
              display: "block",
              fontSize: "0.875rem",
              fontWeight: 500,
              color: "#374151",
              marginBottom: "0.375rem",
            }}
          >
            Alert me when balance drops below
          </label>
          <div style={{ display: "flex", gap: "0.5rem", alignItems: "center" }}>
            <input
              id="threshold-credits"
              type="number"
              min={1}
              value={thresholdCredits}
              onChange={(e) => {
                setThresholdCredits(e.target.value);
                setSaved(false);
                setError(null);
              }}
              placeholder="e.g. 500000"
              style={{
                padding: "0.5rem 0.75rem",
                fontSize: "0.875rem",
                border: "1px solid #d1d5db",
                borderRadius: "0.375rem",
                width: "160px",
              }}
            />
            <span style={{ fontSize: "0.875rem", color: "#6b7280", fontWeight: 500 }}>
              Hive Credits
            </span>
          </div>
        </div>
        <p style={{ margin: "0 0 1rem", fontSize: "0.875rem", color: "#6b7280" }}>
          Alerts are notifications only. Requests continue until your balance reaches zero.
        </p>
        {error && (
          <p style={{ margin: "0 0 0.75rem", fontSize: "0.875rem", color: "#dc2626" }}>{error}</p>
        )}
        {saved && (
          <p style={{ margin: "0 0 0.75rem", fontSize: "0.875rem", color: "#16a34a" }}>
            Alert threshold saved.
          </p>
        )}
        <button
          type="submit"
          disabled={loading || !thresholdCredits}
          style={{
            padding: "0.5rem 1rem",
            fontSize: "0.875rem",
            fontWeight: 600,
            borderRadius: "0.375rem",
            border: "none",
            cursor: loading || !thresholdCredits ? "not-allowed" : "pointer",
            backgroundColor: loading || !thresholdCredits ? "#e5e7eb" : "#1d4ed8",
            color: loading || !thresholdCredits ? "#9ca3af" : "#ffffff",
          }}
        >
          {loading ? "Saving…" : "Save alert"}
        </button>
      </form>
    </div>
  );
}
