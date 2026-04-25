"use client";

import { useState, type FormEvent } from "react";

import type { BudgetThreshold } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";

interface BudgetAlertFormProps {
  currentThreshold: BudgetThreshold | null;
}

interface BudgetErrorBody {
  error?: string;
}

function readErrorMessage(value: unknown, fallback: string): string {
  if (value === null || typeof value !== "object") return fallback;
  const candidate = value as BudgetErrorBody;
  return typeof candidate.error === "string" ? candidate.error : fallback;
}

export function BudgetAlertForm({ currentThreshold }: BudgetAlertFormProps) {
  const [thresholdCredits, setThresholdCredits] = useState<string>(
    currentThreshold ? String(currentThreshold.threshold_credits) : "",
  );
  const [loading, setLoading] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const parsed = parseInt(thresholdCredits, 10);
    if (Number.isNaN(parsed) || parsed <= 0) {
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
        setError(readErrorMessage(data, "Failed to save alert."));
        return;
      }

      setSaved(true);
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : "Network error. Please try again.";
      setError(message);
    } finally {
      setLoading(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Spend alerts</CardTitle>
        <CardDescription>
          Get notified when your balance drops below a threshold. Requests keep
          flowing — alerts are advisory only.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        <form
          onSubmit={(e) => void handleSubmit(e)}
          className="grid gap-4 sm:grid-cols-[200px_auto] sm:items-end"
        >
          <Field
            label="Alert threshold"
            htmlFor="threshold-credits"
            hint="In Hive credits"
          >
            <Input
              id="threshold-credits"
              type="number"
              min={1}
              value={thresholdCredits}
              onChange={(e) => {
                setThresholdCredits(e.target.value);
                setSaved(false);
                setError(null);
              }}
              placeholder="500000"
              className="tabular-nums"
            />
          </Field>
          <Button
            type="submit"
            variant="primary"
            size="md"
            disabled={loading || !thresholdCredits}
          >
            {loading ? "Saving…" : "Save alert"}
          </Button>
          {error ? (
            <p
              role="alert"
              className="text-xs text-[var(--color-danger)] sm:col-span-2"
            >
              {error}
            </p>
          ) : null}
          {saved ? (
            <p
              role="status"
              className="text-xs text-[var(--color-success)] sm:col-span-2"
            >
              Alert threshold saved.
            </p>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}
