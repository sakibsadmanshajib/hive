"use client";

import * as React from "react";
import { AlertCircle, Info } from "lucide-react";

import { cn } from "@/lib/cn";
import type { FeatureGate } from "@/lib/control-plane/client";

interface FeatureGateManagerProps {
  gates: FeatureGate[];
}

type RowStatus = "idle" | "saving" | "error";

// Nicer section headings for known categories; unknown categories fall back to
// a title-cased version of the raw category so a new gate group added by a
// migration still renders sensibly without a code change.
// There is deliberately no `audit_sink` entry, and the dead `audit` entry that
// used to sit here (it never matched: the seeded category was `audit_sink`, so
// the heading fell through to formatCategory anyway) is gone with it. Issue
// #755 retired those six gates from the registry, so the category no longer
// reaches this component at all.
const CATEGORY_LABELS: Record<string, string> = {
  billing: "Billing & payments",
  agents: "Sovereign workspace",
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
  // Per-row failure text from the server, empty when the response carried
  // none. Kept beside status rather than inside it so the generic fallback
  // copy still renders when a request fails before any body exists.
  const [errors, setErrors] = React.useState<Record<string, string>>({});

  async function toggle(gate: FeatureGate): Promise<void> {
    const next = !gate.enabled;

    // Optimistic flip; revert on failure.
    setGates((prev) =>
      prev.map((g) => (g.key === gate.key ? { ...g, enabled: next } : g)),
    );
    setStatus((prev) => ({ ...prev, [gate.key]: "saving" }));
    setErrors((prev) => ({ ...prev, [gate.key]: "" }));

    try {
      const response = await fetch("/api/console/feature-gates", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: gate.key, enabled: next }),
      });
      if (!response.ok) {
        // The route already computes a specific, customer-safe message per
        // status (`gateErrorMessage` in app/api/console/feature-gates/route.ts)
        // and discarding it left every failure reading "try again", which
        // invites a retry that cannot succeed. A key the registry no longer
        // carries answers 400 permanently, which is now a reachable state for
        // anyone holding a bookmarked or scripted call to one of the six audit
        // sink keys retired in issue #755.
        const failure: { error?: string } = await response.json().catch(() => ({}));
        throw new Error(failure.error ?? "");
      }
      // Reconcile with the state the server actually applied, in case it
      // diverged from the request (e.g. a concurrent admin edit), rather than
      // trusting the optimistic value.
      const result: { key?: string; enabled?: boolean } = await response.json();
      const appliedKey = result.key ?? gate.key;
      const applied = typeof result.enabled === "boolean" ? result.enabled : next;
      setGates((prev) =>
        prev.map((g) => (g.key === appliedKey ? { ...g, enabled: applied } : g)),
      );
      setStatus((prev) => ({ ...prev, [gate.key]: "idle" }));
    } catch (err) {
      const message = err instanceof Error ? err.message.trim() : "";
      setGates((prev) =>
        prev.map((g) => (g.key === gate.key ? { ...g, enabled: gate.enabled } : g)),
      );
      setStatus((prev) => ({ ...prev, [gate.key]: "error" }));
      setErrors((prev) => ({ ...prev, [gate.key]: message }));
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
                        {errors[gate.key] || "Could not save. Try again."}
                      </span>
                    ) : null}
                    {gate.enforced !== true ? (
                      // Read as "anything but true", not as "=== false", so a
                      // control-plane that predates the enforced field lands on
                      // the safe side during a rolling deploy.
                      <span className="mt-1 flex items-start gap-1 text-2xs text-[var(--color-ink-3)]">
                        <Info size={12} className="mt-px shrink-0" aria-hidden="true" />
                        <span>
                          Not enforced yet. This setting is saved for this
                          workspace, and no part of the API or apps reads it, so
                          changing it does not change what the workspace can do.
                        </span>
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
                    {gate.manageable ? (
                      <GateSwitch
                        checked={gate.enabled}
                        saving={rowStatus === "saving"}
                        label={gate.label}
                        onToggle={() => {
                          void toggle(gate);
                        }}
                      />
                    ) : (
                      // An unmanageable gate is platform-admin only (issue
                      // #758), so the label names the platform rather than
                      // telling the reader to ask "your administrator", who on
                      // a single-member workspace does not exist (issue #1660).
                      <span className="text-2xs text-[var(--color-ink-3)]">
                        Managed by the platform
                      </span>
                    )}
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
