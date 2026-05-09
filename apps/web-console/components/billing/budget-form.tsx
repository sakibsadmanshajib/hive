"use client";

// Phase 14 FIX-14-25 — workspace budget settings form (BDT-only).
//
// Renders the soft-cap + hard-cap fields for the current workspace, posts
// PUT /api/v1/budgets/{workspace_id} via the typed client. Owner-only on the
// backend; this component takes a `readOnly` flag from the page so non-owners
// see disabled fields instead of a write-then-403 round-trip.
//
// All amounts are BDT subunits (paisa). The form accepts a decimal taka value
// (e.g. "1000.00") and converts to subunits client-side. No USD/FX strings
// anywhere — regulatory rule (feedback_bdt_no_fx_display.md).

import { useState, type FormEvent } from "react";

import type { BudgetSettings } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";

interface BudgetFormProps {
  workspaceId: string;
  budget: BudgetSettings | null;
  readOnly: boolean;
}

interface BudgetErrorBody {
  error?: string;
}

function readErrorMessage(value: unknown, fallback: string): string {
  if (value === null || typeof value !== "object") return fallback;
  const candidate = value as BudgetErrorBody;
  return typeof candidate.error === "string" ? candidate.error : fallback;
}

// Convert BDT subunits (integer) to display taka (decimal string with 2dp).
export function subunitsToTaka(subunits: number): string {
  if (!Number.isFinite(subunits) || subunits < 0) return "0.00";
  const integer = Math.floor(subunits / 100);
  const fraction = subunits % 100;
  return `${integer}.${String(fraction).padStart(2, "0")}`;
}

// Parse user input ("1000", "1000.50", "1000.5") into BDT subunits. Returns
// null when the value cannot be represented as a non-negative subunit count.
export function takaToSubunits(input: string): number | null {
  const trimmed = input.trim();
  if (trimmed === "") return null;
  if (!/^\d+(?:\.\d{0,2})?$/.test(trimmed)) return null;
  const [intPart, fracPart = ""] = trimmed.split(".");
  const fracPadded = (fracPart + "00").slice(0, 2);
  const intValue = Number.parseInt(intPart, 10);
  const fracValue = Number.parseInt(fracPadded, 10);
  if (!Number.isFinite(intValue) || !Number.isFinite(fracValue)) return null;
  const subunits = intValue * 100 + fracValue;
  if (!Number.isSafeInteger(subunits)) return null;
  return subunits;
}

export function BudgetForm({ workspaceId, budget, readOnly }: BudgetFormProps) {
  const [softCap, setSoftCap] = useState<string>(
    budget ? subunitsToTaka(budget.soft_cap_bdt_subunits) : "",
  );
  const [hardCap, setHardCap] = useState<string>(
    budget ? subunitsToTaka(budget.hard_cap_bdt_subunits) : "",
  );
  const [loading, setLoading] = useState<boolean>(false);
  const [saved, setSaved] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (readOnly) {
      setError("Only the workspace owner can change budget caps.");
      return;
    }
    const softSubunits = takaToSubunits(softCap);
    const hardSubunits = takaToSubunits(hardCap);
    if (softSubunits === null || hardSubunits === null) {
      setError("Enter both caps as non-negative numbers (e.g. 1000.00).");
      return;
    }
    if (softSubunits > hardSubunits) {
      setError("Soft cap must be less than or equal to hard cap.");
      return;
    }
    setLoading(true);
    setError(null);
    setSaved(false);
    try {
      const response = await fetch(`/api/budget/${workspaceId}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          soft_cap_bdt_subunits: softSubunits,
          hard_cap_bdt_subunits: hardSubunits,
        }),
      });
      if (!response.ok) {
        const data: unknown = await response.json().catch(() => null);
        setError(readErrorMessage(data, "Failed to save budget."));
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
        <CardTitle>Workspace budget</CardTitle>
        <CardDescription>
          Set a soft cap (advisory alerts) and a hard cap (requests blocked
          beyond this amount). Amounts are in Bangladeshi taka.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        <form
          onSubmit={(e) => void handleSubmit(e)}
          className="grid gap-4 sm:grid-cols-2"
        >
          <Field label="Soft cap (BDT)" htmlFor="budget-soft-cap" hint="Advisory threshold">
            <Input
              id="budget-soft-cap"
              type="text"
              inputMode="decimal"
              value={softCap}
              onChange={(e) => {
                setSoftCap(e.target.value);
                setSaved(false);
                setError(null);
              }}
              placeholder="1000.00"
              disabled={readOnly}
              className="tabular-nums"
            />
          </Field>
          <Field label="Hard cap (BDT)" htmlFor="budget-hard-cap" hint="Requests blocked beyond this">
            <Input
              id="budget-hard-cap"
              type="text"
              inputMode="decimal"
              value={hardCap}
              onChange={(e) => {
                setHardCap(e.target.value);
                setSaved(false);
                setError(null);
              }}
              placeholder="2000.00"
              disabled={readOnly}
              className="tabular-nums"
            />
          </Field>
          <div className="sm:col-span-2 flex items-center gap-3">
            <Button
              type="submit"
              variant="primary"
              size="md"
              disabled={loading || readOnly}
            >
              {loading ? "Saving…" : "Save budget"}
            </Button>
            {readOnly ? (
              <p className="text-xs text-[var(--color-ink-3)]">
                Only the workspace owner can edit budget caps.
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
              Budget saved.
            </p>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}
