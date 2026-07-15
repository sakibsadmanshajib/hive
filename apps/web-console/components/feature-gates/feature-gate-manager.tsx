"use client";

import * as React from "react";
import { AlertCircle } from "lucide-react";

import { cn } from "@/lib/cn";
import type { FeatureGate } from "@/lib/control-plane/client";

interface FeatureGateManagerProps {
  gates: FeatureGate[];
}

type RowStatus = "idle" | "saving" | "error";

// Nicer section headings for known categories; unknown categories fall back to
// a title-cased version of the raw category so a new gate group added by a
// migration still renders sensibly without a code change.
const CATEGORY_LABELS: Record<string, string> = {
  billing: "Billing & payments",
  carl: "Sovereign workspace",
  audit: "Audit sinks",
  sso: "Single sign-on",
  feature: "Platform features",
};

function formatCategory(category: string): string {
  const known = CATEGORY_LABELS[category];
  if (known) {
    return known;
  }
  const spaced = category.replace(/_/g, " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

interface GateGroup {
  category: string;
  gates: FeatureGate[];
}

// groupByCategory keeps the server's (category, label) order: gates arrive
// pre-sorted, so first-seen category order is the display order.
function groupByCategory(gates: FeatureGate[]): GateGroup[] {
  const groups: GateGroup[] = [];
  for (const gate of gates) {
    const existing = groups.find((group) => group.category === gate.category);
    if (existing) {
      existing.gates = [...existing.gates, gate];
    } else {
      groups.push({ category: gate.category, gates: [gate] });
    }
  }
  return groups;
}

export function FeatureGateManager({ gates: initialGates }: FeatureGateManagerProps) {
  const [gates, setGates] = React.useState<FeatureGate[]>(initialGates);
  const [status, setStatus] = React.useState<Record<string, RowStatus>>({});

  async function toggle(gate: FeatureGate): Promise<void> {
    const next = !gate.enabled;

    // Optimistic flip; revert on failure.
    setGates((prev) =>
      prev.map((g) => (g.key === gate.key ? { ...g, enabled: next } : g)),
    );
    setStatus((prev) => ({ ...prev, [gate.key]: "saving" }));

    try {
      const response = await fetch("/api/console/feature-gates", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: gate.key, enabled: next }),
      });
      if (!response.ok) {
        throw new Error("request failed");
      }
      setStatus((prev) => ({ ...prev, [gate.key]: "idle" }));
    } catch {
      setGates((prev) =>
        prev.map((g) => (g.key === gate.key ? { ...g, enabled: gate.enabled } : g)),
      );
      setStatus((prev) => ({ ...prev, [gate.key]: "error" }));
    }
  }

  const groups = groupByCategory(gates);

  return (
    <div className="flex flex-col gap-10">
      {groups.map((group) => (
        <section key={group.category} className="flex flex-col gap-3">
          <h2 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
            {formatCategory(group.category)}
          </h2>
          <ul className="flex flex-col rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] divide-y divide-[var(--color-border)]">
            {group.gates.map((gate) => {
              const rowStatus = status[gate.key] ?? "idle";
              return (
                <li
                  key={gate.key}
                  className="flex items-center justify-between gap-4 px-4 py-3.5"
                >
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <span className="text-sm text-[var(--color-ink)]">
                      {gate.label}
                    </span>
                    <span className="font-mono text-2xs text-[var(--color-ink-3)]">
                      {gate.key}
                    </span>
                    {rowStatus === "error" ? (
                      <span className="mt-0.5 flex items-center gap-1 text-2xs text-[var(--color-danger,#d64545)]">
                        <AlertCircle size={12} />
                        Could not save. Try again.
                      </span>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-3">
                    <span
                      className={cn(
                        "text-2xs tabular-nums transition-opacity",
                        rowStatus === "saving"
                          ? "text-[var(--color-ink-3)] opacity-100"
                          : "opacity-0",
                      )}
                      aria-hidden="true"
                    >
                      Saving…
                    </span>
                    <GateSwitch
                      checked={gate.enabled}
                      saving={rowStatus === "saving"}
                      label={gate.label}
                      onToggle={() => {
                        void toggle(gate);
                      }}
                    />
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}

interface GateSwitchProps {
  checked: boolean;
  saving: boolean;
  label: string;
  onToggle: () => void;
}

function GateSwitch({ checked, saving, label, onToggle }: GateSwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={`${label}: ${checked ? "enabled" : "disabled"}`}
      disabled={saving}
      onClick={onToggle}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 items-center rounded-full",
        "transition-colors duration-[var(--duration-fast)]",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-surface)]",
        checked
          ? "bg-[var(--color-accent)]"
          : "bg-[var(--color-border-strong,#c9c9c9)]",
        saving ? "cursor-wait opacity-70" : "cursor-pointer",
      )}
    >
      <span
        className={cn(
          "inline-block h-5 w-5 transform rounded-full bg-white shadow-sm",
          "transition-transform duration-[var(--duration-fast)]",
          checked ? "translate-x-[22px]" : "translate-x-0.5",
        )}
      />
    </button>
  );
}
