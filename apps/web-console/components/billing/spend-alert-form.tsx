"use client";

// Phase 14 FIX-14-26 — workspace spend-alert form (BDT-only).
//
// Renders the create-alert form: threshold (50/80/100), email, optional
// webhook URL. Posts POST /api/v1/spend-alerts/{workspace_id}. Owner-only on
// the backend. The list table is rendered server-side by the parent page.
//
// No USD/FX strings — regulatory rule (feedback_bdt_no_fx_display.md).

import { useState, type FormEvent } from "react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";

interface SpendAlertFormProps {
  workspaceId: string;
  readOnly: boolean;
  existingThresholds: ReadonlyArray<number>;
}

interface AlertErrorBody {
  error?: string;
}

function readErrorMessage(value: unknown, fallback: string): string {
  if (value === null || typeof value !== "object") return fallback;
  const candidate = value as AlertErrorBody;
  return typeof candidate.error === "string" ? candidate.error : fallback;
}

const THRESHOLD_CHOICES: ReadonlyArray<{ value: number; label: string }> = [
  { value: 50, label: "50% of soft cap" },
  { value: 80, label: "80% of soft cap" },
  { value: 100, label: "100% of soft cap" },
];

export function SpendAlertForm({
  workspaceId,
  readOnly,
  existingThresholds,
}: SpendAlertFormProps) {
  const [threshold, setThreshold] = useState<number>(50);
  const [email, setEmail] = useState<string>("");
  const [webhookUrl, setWebhookUrl] = useState<string>("");
  const [loading, setLoading] = useState<boolean>(false);
  const [saved, setSaved] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (readOnly) {
      setError("Only the workspace owner can create spend alerts.");
      return;
    }
    if (existingThresholds.includes(threshold)) {
      setError(`A ${threshold}% alert already exists for this workspace.`);
      return;
    }
    if (!email && !webhookUrl) {
      setError("Provide at least one delivery channel (email or webhook URL).");
      return;
    }
    setLoading(true);
    setError(null);
    setSaved(false);
    try {
      const response = await fetch(`/api/spend-alerts/${workspaceId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          threshold_pct: threshold,
          email: email || null,
          webhook_url: webhookUrl || null,
        }),
      });
      if (!response.ok) {
        const data: unknown = await response.json().catch(() => null);
        setError(readErrorMessage(data, "Failed to create alert."));
        return;
      }
      setSaved(true);
      setEmail("");
      setWebhookUrl("");
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
        <CardTitle>Create spend alert</CardTitle>
        <CardDescription>
          Send notifications when month-to-date spend reaches a percentage of
          your soft cap. Alerts are advisory; the hard cap controls blocking.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        <form
          onSubmit={(e) => void handleSubmit(e)}
          className="grid gap-4 sm:grid-cols-2"
        >
          <Field label="Threshold" htmlFor="alert-threshold" hint="When to fire">
            <select
              id="alert-threshold"
              value={threshold}
              onChange={(e) => {
                setThreshold(Number.parseInt(e.target.value, 10));
                setSaved(false);
                setError(null);
              }}
              disabled={readOnly}
              className="h-9 w-full rounded-md border border-[var(--color-border)] bg-[var(--color-bg)] px-3 text-sm text-[var(--color-ink)]"
            >
              {THRESHOLD_CHOICES.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Notify email" htmlFor="alert-email" hint="Optional">
            <Input
              id="alert-email"
              type="email"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
                setSaved(false);
                setError(null);
              }}
              placeholder="alerts@example.com"
              disabled={readOnly}
            />
          </Field>
          <Field
            label="Webhook URL"
            htmlFor="alert-webhook"
            hint="Optional — POST notification"
          >
            <Input
              id="alert-webhook"
              type="url"
              value={webhookUrl}
              onChange={(e) => {
                setWebhookUrl(e.target.value);
                setSaved(false);
                setError(null);
              }}
              placeholder="https://example.com/hooks/spend"
              disabled={readOnly}
            />
          </Field>
          <div className="sm:col-span-2 flex items-center gap-3">
            <Button
              type="submit"
              variant="primary"
              size="md"
              disabled={loading || readOnly}
            >
              {loading ? "Creating…" : "Create alert"}
            </Button>
            {readOnly ? (
              <p className="text-xs text-[var(--color-ink-3)]">
                Only the workspace owner can create spend alerts.
              </p>
            ) : null}
          </div>
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
              Alert created.
            </p>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}
